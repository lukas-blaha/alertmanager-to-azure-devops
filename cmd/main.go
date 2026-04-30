package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"

	"github.com/lukas-blaha/alertmanager-to-azure-devops/pkg/parser"
)

const (
	webPort = 8080
	baseUrl = "https://dev.azure.com"
)

type Config struct {
	Org            string
	Project        string
	WorkItem       string
	CreateTemplate *template.Template
	CloseTemplate  string
	Pat            string
	Token          string
	SpId           string
	SpSecret       string
	SpTenant       string
	Debug          bool
	SendEnabled    bool
}

type Token struct {
	Token string `json:"access_token"`
}

func main() {
	debugVar := false
	sendEnabledVar := true

	orgEnv := os.Getenv("ORGANIZATION")
	projectEnv := os.Getenv("PROJECT")
	workItemEnv := os.Getenv("WORKITEM")
	createTmplEnv := os.Getenv("CREATE_TEMPLATE")
	closeTmplEnv := os.Getenv("CLOSE_TEMPLATE")
	tokenEnv := os.Getenv("TOKEN")
	spIdEnv := os.Getenv("SP_ID")
	spSecretEnv := os.Getenv("SP_SECRET")
	spTenantEnv := os.Getenv("SP_TENANT")
	debugEnv := os.Getenv("DEBUG")
	enabledEnv := os.Getenv("SENDING_ENABLED")

	if strings.ToLower(debugEnv) == "true" {
		debugVar = true
	}

	if strings.ToLower(enabledEnv) == "false" {
		sendEnabledVar = false
	}

	var org string
	flag.StringVar(&org, "organization", orgEnv, "Azure DevOps organization name")

	var project string
	flag.StringVar(&project, "project", projectEnv, "Azure DevOps project name")

	var workItem string
	flag.StringVar(&workItem, "workitem", workItemEnv, "Azure DevOps work item name")

	var createTmplPath string
	flag.StringVar(&createTmplPath, "create-template", createTmplEnv, "Path to payload transformation template to create ticket")

	var closeTmplPath string
	flag.StringVar(&closeTmplPath, "close-template", closeTmplEnv, "Path to payload transformation template to close ticket")

	var pat string
	flag.StringVar(&pat, "token", tokenEnv, "Authorization token")

	var spId string
	flag.StringVar(&spId, "sp-id", spIdEnv, "Service principal client ID")

	var spSecret string
	flag.StringVar(&spSecret, "sp-secret", spSecretEnv, "Service principal secret")

	var spTenant string
	flag.StringVar(&spTenant, "sp-tenant", spTenantEnv, "Service principal tenant ID")

	var debug bool
	flag.BoolVar(&debug, "debug", debugVar, "Enable debug mode")

	var sendEnabled bool
	flag.BoolVar(&sendEnabled, "enabled", sendEnabledVar, "Enable sending alerts")

	flag.Parse()

	if pat == "" {
		if spId == "" || spSecret == "" || spTenant == "" {
			log.Panicf("You have to provide PAT token or service principal creadentials to authenticate.")
		}
	} else if org == "" || project == "" || workItem == "" || createTmplPath == "" || closeTmplPath == "" {
		msg := "env ORG or -organization\nenv PROJECT or -project\nenv WORKITEM or -workitem\nenv CREATE_TEMPLATE or -create-template\nenv CLOSE_TEMPLATE or -close-template\nenv TOKEN or -token"
		log.Panicf("Missing required flags or environment variables. See settings below:\n\n%s", msg)
	}

	createTmpl, err := parser.New(createTmplPath)
	if err != nil {
		log.Panic(err)
	}

	closeTmpl, err := os.ReadFile(closeTmplPath)
	if err != nil {
		log.Panic(err)
	}

	app := Config{
		Org:            org,
		Project:        project,
		WorkItem:       workItem,
		CreateTemplate: createTmpl,
		CloseTemplate:  string(closeTmpl),
		Pat:            pat,
		SpId:           spId,
		SpSecret:       spSecret,
		SpTenant:       spTenant,
		Debug:          debug,
		SendEnabled:    sendEnabled,
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", webPort),
		Handler: app.routes(),
	}

	fmt.Println("Server is running on port", webPort)

	if err := srv.ListenAndServe(); err != nil {
		log.Panic("Cannot start server:", err)
	}
}
