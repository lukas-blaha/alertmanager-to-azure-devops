package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/lukas-blaha/alertmanager-to-azure-devops/pkg/grafana"
	"github.com/lukas-blaha/alertmanager-to-azure-devops/pkg/parser"
	amt "github.com/prometheus/alertmanager/template"
)

func (app *Config) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /", app.GetTemplate)

	return mux
}

func (app *Config) GetTemplate(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	if app.Debug {
		log.Println("Alert body:", string(b))
	}

	rd := bytes.NewReader(b)

	data, err := Decode(rd)
	if err != nil {
		log.Println("Cannot decode request:", err)
		return
	}

	// Support for alerts without instance label (mostly from ElasticSearch datasource and Sloth SLOs)
	for i := range data.Alerts {
		if _, ok := data.Alerts[i].Labels["instance"]; !ok {
			if hostname_keyword, ok := data.Alerts[i].Labels["host.hostname.keyword"]; ok {
				data.Alerts[i].Labels["instance"] = hostname_keyword
			} else if hostname, ok := data.Alerts[i].Labels["host.hostname"]; ok {
				data.Alerts[i].Labels["instance"] = hostname
			} else if sloth_slo, ok := data.Alerts[i].Labels["sloth_slo"]; ok {
				data.Alerts[i].Labels["instance"] = sloth_slo
			} else {
				log.Println("No instance label found in alert:", data.Alerts[i].Labels)
				data.Alerts[i].Labels["instance"] = "unknown"
			}
		}
	}

	for i := range data.Alerts {
		if summary, ok := data.Alerts[i].Annotations["summary"]; ok {
			data.Alerts[i].Annotations["summary"] = strings.ReplaceAll(summary, "\n", "<br>")
		}
	}

	if summary, ok := data.CommonAnnotations["summary"]; ok {
		data.CommonAnnotations["summary"] = strings.ReplaceAll(summary, "\n", "<br>")
	}

	templatingCache := make(map[string][]grafana.TemplatingItem)

	// For each alert, build a list of dashboard variables and their values
	alertDashboardVars := make([][]parser.DashboardVar, len(data.Alerts))

	for i, alert := range data.Alerts {
		dashboardUid, ok := alert.Annotations["__dashboardUid__"]
		if !ok || dashboardUid == "" {
			continue // No dashboard UID, skip this alert
		}

		// Get templating info, use per-request cache to avoid duplicate API calls
		var templatingItems []grafana.TemplatingItem
		if items, found := templatingCache[dashboardUid]; found {
			templatingItems = items
		} else {
			items, err := grafana.GetTemplatingFromDashboard(app.GrafanaUrl, app.GrafanaToken, dashboardUid)
			if err != nil {
				log.Println("Could not get dashboard templating info:", err)
				continue
			}
			templatingCache[dashboardUid] = items
			templatingItems = items
		}

		// Build a map of alert labels for fast lookup
		alertLabels := alert.Labels // map[string]string

		// Match dashboard variables to alert labels
		var dashboardVars []parser.DashboardVar
		for _, item := range templatingItems {
			for _, label := range item.Labels {
				if value, ok := alertLabels[label]; ok {
					dashboardVars = append(dashboardVars, parser.DashboardVar{
						Name:  item.Name,
						Value: value,
					})
					break // Only one value per dashboard variable
				}
			}
		}
		alertDashboardVars[i] = dashboardVars
	}

	templateData := parser.TemplateData{
		Data:               data,
		AlertDashboardVars: alertDashboardVars,
	}

	s, err := parser.Render(app.CreateTemplate, templateData)
	if err != nil {
		log.Println("Cannot render template:", err)
		return
	}

	err = app.Authenticate()
	if err != nil {
		log.Println("Cannot authenticate:", err)
		return
	} else {
		if app.Debug {
			log.Println("Authenticated successfully.")
		}
	}

	ticket, err := app.GetTicket(data.Alerts[0].Fingerprint)
	if err != nil {
		log.Println("Could not get ticket:", err)
		return
	} else {
		if app.Debug {
			log.Println("Ticket found:", ticket)
		}
	}

	ticket, err = app.GetTicketByUrl(ticket.Url)
	if err != nil {
		log.Println("Could not get ticket by URL:", err)
	} else {
		if app.Debug {
			log.Println("Ticket found by URL:", ticket)
		}
	}

	switch data.Alerts[0].Status {
	case "firing":
		if app.Debug {
			log.Println("Alert status is firing, checking ticket...")
			log.Println("Ticket: ", ticket)
		}
		if ticket == (Ticket{}) {
			if app.SendEnabled {
				if app.Debug {
					log.Println("Creating ticket for grafana alert:", data.Alerts[0].Fingerprint)
				}
				err = app.CreateTicket(s)
				if err != nil {
					log.Println("Cannot create ticket:", err)
					return
				}
			} else {
				log.Println("Ticket creation is disabled. Skipping ticket creation.")
				return
			}
		} else {
			// Existing ticket found
			if ticket.State == "Done" || ticket.State == "Removed" {
				// Create new ticket and add comment about the old one
				if app.SendEnabled {
					fmt.Println("Existing ticket is Done or Removed, creating new ticket for Grafana alert:", data.Alerts[0].Fingerprint)
					err = app.CreateTicket(s)
					if err != nil {
						log.Println("Cannot create ticket:", err)
						return
					}
					// After creating, get the new ticket (by fingerprint)
					newTicket, err := app.GetTicket(data.Alerts[0].Fingerprint)
					if err != nil {
						log.Println("Cannot get new ticket after creation:", err)
						return
					}
					newTicket, err = app.GetTicketByUrl(newTicket.Url)
					if err != nil {
						log.Println("Cannot get new ticket by URL:", err)
						return
					}

					ticket_url := fmt.Sprintf(
						"https://%s.%s/%s/%s/%d",
						replaceBlanks(app.Org),
						"visualstudio.com",
						replaceBlanks(app.Project),
						"_workitems/edit",
						ticket.Id,
					)
					// Add comment to new ticket about the old one
					comment := fmt.Sprintf(`Last PBI with same alert: <a href="%s">%s</a>`, ticket_url, data.Alerts[0].Labels["alertname"])
					if app.Debug {
						log.Println("Adding comment to new ticket:", comment)
					}
					err = app.AddComment(newTicket, comment)
					if err != nil {
						log.Println("Cannot add comment to new ticket:", err)
					}
				} else {
					log.Println("Ticket creation is disabled. Skipping ticket creation.")
					return
				}
			} else {
				log.Println("Ticket already exists for this alert, skipping ticket creation.")
			}
		}
	case "resolved":
		if app.Debug {
			log.Println("Alert status is resolved, checking ticket...")
			log.Println("Ticket: ", ticket)
		}
		if ticket != (Ticket{}) {
			if app.SendEnabled {
				fmt.Println("Closing ticket for grafana alert:", data.Alerts[0].Fingerprint)
				err = app.CloseTicket(ticket, data.Alerts[0])
				if err != nil {
					log.Println("Cannot close ticket:", err)
					return
				}
			} else {
				log.Println("Ticket closing is disabled. Skipping ticket closing.")
				return
			}
		} else {
			log.Println("No ticket found for this alert, skipping ticket closing.")
		}
	}
}

func (app *Config) Authenticate() error {
	if app.Pat != "" {
		if app.Debug {
			log.Println("Using PAT token authentication:", app.Token)
		}

		app.Token = app.Pat
		return nil
	} else {
		if app.Debug {
			log.Println("Using service principal authentication.")
		}

		var token Token
		url := fmt.Sprintf("https://login.microsoft.com/%s/oauth2/v2.0/token", app.SpTenant)
		scope := "499b84ac-1321-427f-aa17-267ca6975798/.default"

		payload := fmt.Sprintf(`client_id=%s
					&scope=%s
					&client_secret=%s
					&grant_type=client_credentials`,
			app.SpId, scope, app.SpSecret)

		req, err := http.NewRequest("POST", url, bytes.NewReader([]byte(payload)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		err = decoder.Decode(&token)
		if err != nil {
			return err
		}
		app.Token = token.Token
	}

	return nil
}

func Decode(body io.Reader) (amt.Data, error) {
	var data amt.Data

	if body == nil {
		return amt.Data{}, nil
	}

	decoder := json.NewDecoder(body)

	err := decoder.Decode(&data)

	return data, err
}
