package grafana

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sync"
)

// TemplatingItem holds the dashboard variable name and all referenced labels
type TemplatingItem struct {
	Name   string
	Labels []string
}

var (
	dashboardTemplatingCache = make(map[string][]TemplatingItem)
	cacheMutex               sync.RWMutex
)

// Extract all labels from a query string like label_values(metric, label)
func extractLabelsFromQuery(query string) []string {
	re := regexp.MustCompile(`label_values\([^,]+,\s*([^)]+)\)`)
	matches := re.FindAllStringSubmatch(query, -1)
	var labels []string
	for _, m := range matches {
		if len(m) > 1 {
			labels = append(labels, m[1])
		}
	}
	return labels
}

// GetTemplatingFromDashboard fetches and caches templating info for a dashboard UID
func GetTemplatingFromDashboard(grafanaURL, grafanaToken, dashboardUid string) ([]TemplatingItem, error) {
	cacheMutex.RLock()
	if items, ok := dashboardTemplatingCache[dashboardUid]; ok {
		cacheMutex.RUnlock()
		return items, nil
	}
	cacheMutex.RUnlock()

	url := fmt.Sprintf("%s/api/dashboards/uid/%s", grafanaURL, dashboardUid)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+grafanaToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var dashboardResp struct {
		Dashboard struct {
			Templating struct {
				List []struct {
					Name  string          `json:"name"`
					Query json.RawMessage `json:"query"`
				} `json:"list"`
			} `json:"templating"`
		} `json:"dashboard"`
	}

	if err := json.Unmarshal(body, &dashboardResp); err != nil {
		return nil, err
	}

	var items []TemplatingItem
	for _, v := range dashboardResp.Dashboard.Templating.List {
		var queryStr string

		if err := json.Unmarshal(v.Query, &queryStr); err != nil {
			var queryObj struct {
				Query string `json:"query"`
			}
			if err2 := json.Unmarshal(v.Query, &queryObj); err2 != nil {
				log.Printf("Could not parse query field for variable %s: %v", v.Name, err2)
				continue
			}
			queryStr = queryObj.Query
		}

		labels := extractLabelsFromQuery(queryStr)
		if v.Name != "" && len(labels) > 0 {
			items = append(items, TemplatingItem{Name: v.Name, Labels: labels})
		}
	}

	cacheMutex.Lock()
	dashboardTemplatingCache[dashboardUid] = items
	cacheMutex.Unlock()

	return items, nil
}
