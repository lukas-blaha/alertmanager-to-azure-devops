package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	amt "github.com/prometheus/alertmanager/template"
)

type Result struct {
	QueryType string   `json:"queryType"`
	AsOf      string   `json:"asOf"`
	Columns   []Column `json:"columns"`
	WorkItems []Ticket `json:"workItems"`
}

type Column struct {
	Name          string `json:"name"`
	ReferenceName string `json:"referenceName"`
	Url           string `json:"url"`
}

type Ticket struct {
	Id                  int    `json:"id"`
	Url                 string `json:"url"`
	State               string `json:"state"`
	Owner               string `json:"owner"`
	ImplementationNotes string `json:"implementationNotes"`
}

func replaceBlanks(s string) string {
	return strings.Replace(s, " ", "%20", -1)
}

func (app *Config) CreateUrl(op string) string {
	var (
		urlPath string
		params  string
		wrktItm string
	)
	switch op {
	case "create":
		urlPath = "_apis/wit/workitems/$"
		params = "?api-version=7.1"
		wrktItm = replaceBlanks(app.WorkItem)
	case "get":
		urlPath = "_apis/wit/wiql"
		params = "?api-version=6.0"
		wrktItm = ""
	case "update":
		urlPath = "_apis/wit/workitems/"
		params = "?api-version=7.1"
		wrktItm = replaceBlanks(app.WorkItem)
	}

	url := fmt.Sprintf(
		"%s/%s/%s/%s%s%s",
		baseUrl,
		replaceBlanks(app.Org),
		replaceBlanks(app.Project),
		urlPath,
		wrktItm,
		params,
	)

	return url
}

func (app *Config) MakeRequest(method, url, payload, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader([]byte(payload)))
	if err != nil {
		if app.Debug {
			log.Println("Cannot create new request to:", url)
		}
		return nil, err
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", app.Token))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		if app.Debug {
			log.Println("Cannot make request to:", url)
		}
		return nil, err
	}

	return resp, nil
}

func ParseTicketFields(raw map[string]interface{}) Ticket {
	var t Ticket

	// State
	if state, ok := raw["System.State"].(string); ok {
		t.State = state
	}

	// Owner (uniqueName)
	if assignedTo, ok := raw["System.AssignedTo"].(map[string]interface{}); ok {
		if uniqueName, ok := assignedTo["uniqueName"].(string); ok {
			t.Owner = uniqueName
		}
	}

	// ImplementationNotes
	if notes, ok := raw["Custom.ImplementationNotes"].(string); ok {
		t.ImplementationNotes = notes
	}

	return t
}

func (app *Config) CreateTicket(payload string) error {
	// "https://dev.azure.com/<organization>/<project>/_apis/wit/workitems/<workitem>?api-version=7.1"
	url := app.CreateUrl("create")
	if app.Debug {
		log.Printf("Creating ticket with payload: %s", payload)
	}
	resp, err := app.MakeRequest("POST", url, payload, "application/json-patch+json")
	defer resp.Body.Close()

	if app.Debug {
		b, _ := io.ReadAll(resp.Body)
		log.Println("Response body:", string(b))
	}

	if resp.StatusCode != 200 {
		return err
	}

	return nil
}

func (app *Config) GetTicket(id string) (Ticket, error) {
	var result Result

	// "https://dev.azure.com/<organization>/<project>/_apis/wit/wiql?api-version=6.0"
	url := app.CreateUrl("get")

	query := fmt.Sprintf(`{"query": "SELECT [System.Id] FROM WorkItems WHERE [PEScrum.WeblistName] CONTAINS \"%s\" ORDER BY [System.CreatedDate] DESC"}`, id)

	resp, err := app.MakeRequest("POST", url, query, "application/json")
	if err != nil {
		if app.Debug {
			log.Println("Cannot make post request to get VSTS ticket.")
		}
		return Ticket{}, err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)

	err = decoder.Decode(&result)
	if err != nil {
		if app.Debug {
			log.Println("Cannot decode response from get VSTS ticket.")
		}
		return Ticket{}, err
	}

	if resp.StatusCode != 200 {
		if app.Debug {
			log.Println("VSTS response code: ", resp.StatusCode)
		}
		return Ticket{}, err
	}

	if len(result.WorkItems) == 0 {
		if app.Debug {
			log.Println("VSTS ticket not found.")
		}
		return Ticket{}, nil
	}
	return result.WorkItems[0], nil
}

func (app *Config) GetTicketByUrl(url string) (Ticket, error) {
	// Ensure the API version is present
	if !strings.Contains(url, "?api-version=") {
		if strings.Contains(url, "?") {
			url += "&api-version=7.1"
		} else {
			url += "?api-version=7.1"
		}
	}

	resp, err := app.MakeRequest("GET", url, "", "application/json")
	if err != nil {
		return Ticket{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Ticket{}, err
	}

	var ticketRaw struct {
		Id     int                    `json:"id"`
		Url    string                 `json:"url"`
		Fields map[string]interface{} `json:"fields"`
	}
	err = json.Unmarshal(data, &ticketRaw)
	if err != nil {
		return Ticket{}, err
	}

	ticket := ParseTicketFields(ticketRaw.Fields)
	ticket.Id = ticketRaw.Id
	ticket.Url = ticketRaw.Url

	return ticket, nil
}

func (app *Config) AddComment(ticket Ticket, comment string) error {
	if ticket.Id == 0 {
		return fmt.Errorf("ticket ID is missing")
	}
	if ticket.Url == "" {
		return fmt.Errorf("ticket URL is missing")
	}

	// Append the comments API path to the ticket URL
	url := ticket.Url + "/comments?api-version=7.1-preview.3"

	payload := fmt.Sprintf(`{"text": %q}`, comment)
	resp, err := app.MakeRequest("POST", url, payload, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Failed to add comment: %s", string(b))
	}
	return nil
}

func (app *Config) CloseTicket(ticket Ticket, alert amt.Alert) error {
	endsAt := alert.EndsAt
	startsAt := alert.StartsAt
	duration := endsAt.Sub(startsAt)
	comment := fmt.Sprintf("[Grafana] Alert resolved at: %s, duration: %s", endsAt.Format("2006-01-02 15:04:05"), duration)

	closeAfterResolved := alert.Labels["close_after_resolved"]
	if ticket.Owner != "" || closeAfterResolved == "false" {
		log.Println("Ticket has owner or close_after_resolved is false. Adding comment instead of closing.")
		return app.AddComment(ticket, comment)
	}

	closePayload := strings.Replace(app.CloseTemplate, "{{.Comment}}", comment, -1)

	// "https://dev.azure.com/<organization>/<project>/_apis/wit/workitems/<ticket.id>?api-version=7.1"
	url := fmt.Sprintf("%s%s", ticket.Url, "?api-version=7.1")

	resp, err := app.MakeRequest("PATCH", url, closePayload, "application/json-patch+json")
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return err
	}

	return nil
}
