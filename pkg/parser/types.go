package parser

import amt "github.com/prometheus/alertmanager/template"

type TemplateData struct {
	Data               amt.Data
	AlertDashboardVars [][]DashboardVar
}

type DashboardVar struct {
	Name  string
	Value string
}
