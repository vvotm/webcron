package main

import (
	"github.com/astaxie/beego"
	"github.com/loovien/webcron/app/controllers"
	"github.com/loovien/webcron/app/jobs"
	_ "github.com/loovien/webcron/app/mail"
	"github.com/loovien/webcron/app/models"
	"html/template"
	"net/http"
	"fmt"
	"encoding/json"
)

const VERSION = "1.0.0"

func main1()  {
	ccList := [][]string{}
	ccList = append(ccList, []string{"luowenhui@bianfeng.com"})
	proto := struct {
		Cmdid string `json:"cmdid"`
		Title string `json:"title"`
		Body string `json:"body"`
		Tpl string `json:"tpl"`
		ToMail [][]string `json:"toMail"`
	}{Cmdid:"sendmail", Title:"ok", Body:"lahah", Tpl:"common", ToMail:ccList}

	fmt.Println(proto)
	txt, err := json.Marshal(proto)
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println(string(txt))

}

func main() {
	models.Init()
	jobs.InitJobs()

	// 设置默认404页面
	beego.ErrorHandler("404", func(rw http.ResponseWriter, r *http.Request) {
		t, _ := template.New("404.html").ParseFiles(beego.BConfig.WebConfig.ViewsPath + "/error/404.html")
		data := make(map[string]interface{})
		data["content"] = "page not found"
		t.Execute(rw, data)
	})

	// 生产环境不输出debug日志
	if beego.AppConfig.String("runmode") == "prod" {
		beego.SetLevel(beego.LevelInformational)
	}
	beego.AppConfig.Set("version", VERSION)

	// 路由设置
	beego.Router("/", &controllers.MainController{}, "*:Index")
	beego.Router("/login", &controllers.MainController{}, "*:Login")
	beego.Router("/logout", &controllers.MainController{}, "*:Logout")
	beego.Router("/profile", &controllers.MainController{}, "*:Profile")
	beego.Router("/gettime", &controllers.MainController{}, "*:GetTime")
	beego.Router("/help", &controllers.HelpController{}, "*:Index")
	beego.AutoRouter(&controllers.TaskController{})
	beego.AutoRouter(&controllers.GroupController{})

	beego.BConfig.WebConfig.Session.SessionOn = true
	beego.Run()
}
