package jobs

import (
	"bytes"
	"fmt"
	"github.com/astaxie/beego"
	"github.com/loovien/webcron/app/models"
	"text/template"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"
	"runtime"
	"github.com/loovien/webcron/app/libs"
	"encoding/json"
	"github.com/loovien/webcron/app/mail"
)

var mailTpl *template.Template

func init() {
	mailTpl, _ = template.New("mail_tpl").Parse(`
	你好 {{.username}}，<br/>

<p>以下是任务执行结果：</p>

<p>
任务ID：{{.task_id}}<br/>
任务名称：{{.task_name}}<br/>       
执行时间：{{.start_time}}<br />
执行耗时：{{.process_time}}秒<br />
执行状态：{{.status}}
</p>
<p>-------------以下是任务执行输出-------------</p>
<p>{{.output}}</p>

<p>-------------错误日志数输出-----------------</p>
<p>{{.errput}}</p>

--------------------------------------------<br />
本邮件由系统自动发出，请勿回复<br />
如果要取消邮件通知，请登录到系统进行设置<br />
</p>`)

}

type Job struct {
	id         int                                               // 任务ID
	logId      int64                                             // 日志记录ID
	name       string                                            // 任务名称
	task       *models.Task                                      // 任务对象
	runFunc    func(time.Duration) (string, string, error, bool) // 执行函数
	status     int                                               // 任务状态，大于0表示正在执行中
	Concurrent bool                                              // 同一个任务是否允许并行执行
}

func NewJobFromTask(task *models.Task) (*Job, error) {
	if task.Id < 1 {
		return nil, fmt.Errorf("ToJob: 缺少id")
	}
	job := NewCommandJob(task.Id, task.TaskName, task.Command)
	job.task = task
	job.Concurrent = task.Concurrent == 1
	return job, nil
}

func NewCommandJob(id int, name string, command string) *Job {
	job := &Job{
		id:   id,
		name: name,
	}
	job.runFunc = func(timeout time.Duration) (string, string, error, bool) {
		bufOut := new(bytes.Buffer)
		bufErr := new(bytes.Buffer)
		cmd := exec.Command("/bin/bash", "-c", command)
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/C", command)
		}
		cmd.Stdout = bufOut
		cmd.Stderr = bufErr
		cmd.Start()
		err, isTimeout := runCmdWithTimeout(cmd, timeout)

		return bufOut.String(), bufErr.String(), err, isTimeout
	}
	return job
}

func (j *Job) Status() int {
	return j.status
}

func (j *Job) GetName() string {
	return j.name
}

func (j *Job) GetId() int {
	return j.id
}

func (j *Job) GetLogId() int64 {
	return j.logId
}

func (j *Job) Run() {
	if !j.Concurrent && j.status > 0 {
		beego.Warn(fmt.Sprintf("任务[%d]上一次执行尚未结束，本次被忽略。", j.id))
		return
	}

	defer func() {
		if err := recover(); err != nil {
			beego.Error(err, "\n", string(debug.Stack()))
		}
	}()

	if workPool != nil {
		workPool <- true
		defer func() {
			<-workPool
		}()
	}

	beego.Debug(fmt.Sprintf("开始执行任务: %d", j.id))

	j.status++
	defer func() {
		j.status--
	}()

	t := time.Now()
	timeout := time.Duration(time.Hour * 24)
	if j.task.Timeout > 0 {
		timeout = time.Second * time.Duration(j.task.Timeout)
	}

	cmdOut, cmdErr, err, isTimeout := j.runFunc(timeout)

	ut := time.Now().Sub(t) / time.Millisecond

	// 插入日志
	log := new(models.TaskLog)
	log.TaskId = j.id
	log.Output = cmdOut
	log.Error = cmdErr
	log.ProcessTime = int(ut)
	log.CreateTime = t.Unix()

	if isTimeout {
		log.Status = models.TASK_TIMEOUT
		log.Error = fmt.Sprintf("任务执行超过 %d 秒\n----------------------\n%s\n", int(timeout/time.Second), cmdErr)
	} else if err != nil {
		log.Status = models.TASK_ERROR
		log.Error = err.Error() + ":" + cmdErr
	}
	j.logId, _ = models.TaskLogAdd(log)

	// 更新上次执行时间
	j.task.PrevTime = t.Unix()
	j.task.ExecuteTimes++
	j.task.Update("PrevTime", "ExecuteTimes")

	// 发送邮件通知
	if (j.task.Notify == 1 && err != nil) || j.task.Notify == 2 {
		user, uerr := models.UserGetById(j.task.UserId)
		if uerr != nil {
			return
		}

		var title string

		data := make(map[string]interface{})
		data["task_id"] = j.task.Id
		data["username"] = user.UserName
		data["task_name"] = j.task.TaskName
		data["start_time"] = beego.Date(t, "Y-m-d H:i:s")
		data["process_time"] = float64(ut) / 1000
		data["output"] = cmdOut
		data["errput"] = cmdErr

		if isTimeout {
			title = fmt.Sprintf("任务执行结果通知 #%d: %s", j.task.Id, "超时")
			data["status"] = fmt.Sprintf("超时（%d秒）", int(timeout/time.Second))
		} else if err != nil {
			title = fmt.Sprintf("任务执行结果通知 #%d: %s", j.task.Id, "失败")
			data["status"] = "失败（" + err.Error() + "）"
		} else {
			title = fmt.Sprintf("任务执行结果通知 #%d: %s", j.task.Id, "成功")
			data["status"] = "成功"
		}

		content := new(bytes.Buffer)
		mailTpl.Execute(content, data)

		ccList := [][]string{}
		cList := strings.Split(j.task.NotifyEmail, "\n")
		if j.task.NotifyEmail != "" {
			for _, email := range cList {
				ccList = append(ccList, []string{email})
			}
		}
		if !mail.IsUseZqTcpEmail() {
			if !mail.SendMail(user.Email, user.UserName, title, content.String(), cList) {
				beego.Error("发送邮件超时：", user.Email)
			}
			return
		}

		zqutil := libs.NewZqUtil()
		proto := struct {
			Cmdid string `json:"cmdid"`
			Title string `json:"title"`
			Body string `json:"body"`
			Tpl string `json:"tpl"`
			ToMail [][]string `json:"toMail"`
		}{Cmdid:"sendmail", Title:title, Body:content.String(), Tpl:"common", ToMail:ccList}

		emailTxt, err := json.Marshal(proto)
		if err != nil {
			beego.Error("邮件发送失败", proto)
			return
		}
		if zqutil.SendNotifyEmail(string(emailTxt)) != nil {
			beego.Error("邮件协议发送失败")
		}
	}
}
