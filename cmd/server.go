package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

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

	s, err := parser.Render(app.CreateTemplate, data)
	if err != nil {
		log.Println("Cannot render template:", err)
		return
	}

	err = app.Authenticate()
	if err != nil {
		log.Println("Cannot authenticate:", err)
		return
	}

	ticket, err := app.GetTicket(data.Alerts[0].Fingerprint)
	if err != nil {
		log.Println("Could not get ticket:", err)
		return
	}

	if data.Alerts[0].Status == "firing" {
		if ticket == (Ticket{}) {
			if app.SendEnabled {
				fmt.Println("Creating ticket for grafana alert:", data.Alerts[0].Fingerprint)
				err = app.CreateTicket(s)
				if err != nil {
					log.Println("Cannot create ticket:", err)
					return
				}
			} else {
				log.Println("Ticket creation is disabled. Skipping ticket creation.")
				return
			}
		}
	} else if data.Alerts[0].Status == "resolved" {
		if ticket != (Ticket{}) {
			if app.SendEnabled {
				fmt.Println("Closing ticket for grafana alert:", data.Alerts[0].Fingerprint)
				err = app.CloseTicket(ticket)
				if err != nil {
					log.Println("Cannot close ticket:", err)
					return
				}
			} else {
				log.Println("Ticket closing is disabled. Skipping ticket closing.")
				return
			}
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
