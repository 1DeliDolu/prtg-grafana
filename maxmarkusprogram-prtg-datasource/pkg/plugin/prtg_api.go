package plugin

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// Api holds API-related configurations.
type Api struct {
	baseURL string
	apiKey  string
	timeout time.Duration
}

// NewApi creates a new Api instance.
// requestTimeout is used as timeout for API requests.
func NewApi(baseURL, apiKey string, cacheTime, requestTimeout time.Duration) *Api {
	return &Api{
		baseURL: baseURL,
		apiKey:  apiKey,
		timeout: requestTimeout,
	}
}

// buildApiUrl creates a standardized PRTG API URL with given parameters.
func (a *Api) buildApiUrl(method string, params map[string]string) (string, error) {
	baseUrl := fmt.Sprintf("%s/api/%s", a.baseURL, method)
	u, err := url.Parse(baseUrl)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	q := url.Values{}
	q.Set("apitoken", a.apiKey)

	for key, value := range params {
		q.Set(key, value)
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}

// SetTimeout aktualisiert das Timeout für API-Anfragen.
func (a *Api) SetTimeout(timeout time.Duration) {
	if timeout > 0 {
		a.timeout = timeout
	}
}

// baseExecuteRequest führt die HTTP-Anfrage durch und liefert den Response-Body.
func (a *Api) baseExecuteRequest(endpoint string, params map[string]string) ([]byte, error) {
	apiUrl, err := a.buildApiUrl(endpoint, params)
	if err != nil {
		return nil, fmt.Errorf("failed to build URL: %w", err)
	}

	client := &http.Client{
		Timeout: a.timeout,
		Transport: &http.Transport{
			// Warning: InsecureSkipVerify should be reviewed in production environments!
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	req, err := http.NewRequest("GET", apiUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		log.DefaultLogger.Error("Access denied: please verify API token and permissions")
		return nil, fmt.Errorf("access denied: please verify API token and permissions")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	backend.Logger.Debug("Raw response body", "body", string(body))
	return body, nil
}

// GetStatusList ruft die Statusliste der PRTG-API ab.
func (a *Api) GetStatusList() (*PrtgStatusListResponse, error) {
	body, err := a.baseExecuteRequest("status.json", nil)
	if err != nil {
		return nil, err
	}

	var response PrtgStatusListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &response, nil
}

// GetGroups ruft die Gruppenliste ab.
func (a *Api) GetGroups() (*PrtgGroupListResponse, error) {
	params := map[string]string{
		"content": "groups",
		"columns": "active,channel,datetime,device,group,message,objid,priority,sensor,status,tags",
		"count":   "50000",
	}

	body, err := a.baseExecuteRequest("table.json", params)
	if err != nil {
		return nil, err
	}

	var response PrtgGroupListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

// GetDevices ruft die Geräte-Liste ab.
func (a *Api) GetDevices() (*PrtgDevicesListResponse, error) {
	params := map[string]string{
		"content": "devices",
		"columns": "active,channel,datetime,device,group,message,objid,priority,sensor,status,tags",
		"count":   "50000",
	}

	body, err := a.baseExecuteRequest("table.json", params)
	if err != nil {
		return nil, err
	}

	var response PrtgDevicesListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

// GetSensors ruft die Sensoren-Liste ab.
func (a *Api) GetSensors() (*PrtgSensorsListResponse, error) {
	params := map[string]string{
		"content": "sensors",
		"columns": "active,channel,datetime,device,group,message,objid,priority,sensor,status,tags",
		"count":   "50000",
	}

	body, err := a.baseExecuteRequest("table.json", params)
	if err != nil {
		return nil, err
	}

	backend.Logger.Debug("Sensor Response", "body", string(body))

	var response PrtgSensorsListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

// GetChannels ruft die Channel-Werte für die angegebene objid ab.
func (a *Api) GetChannels(objid string) (*PrtgChannelValueStruct, error) {
	params := map[string]string{
		"content":    "values",
		"id":         objid,
		"columns":    "value_,datetime",
		"usecaption": "true",
		"count":      "50000",
	}

	body, err := a.baseExecuteRequest("historicdata.json", params)
	if err != nil {
		return nil, err
	}

	// Optional: Rohantwort in eine Datei schreiben (für Debugging)
	if err := os.WriteFile("channel_response.txt", body, 0644); err != nil {
		backend.Logger.Warn("Could not save channel response to file", "error", err)
	}

	var response PrtgChannelValueStruct
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

// GetHistoricalData ruft historische Daten für den angegebenen Sensor und Zeitraum ab.
func (a *Api) GetHistoricalData(sensorID string, startDate, endDate int64) (*PrtgHistoricalDataResponse, error) {
	backend.Logger.Info("GetHistoricalData called", "sensorID", sensorID, "startDate", startDate, "endDate", endDate)

	if sensorID == "" {
		return nil, fmt.Errorf("invalid query: missing sensor ID")
	}

	startTime := time.UnixMilli(startDate)
	endTime := time.UnixMilli(endDate)

	const format = "2006-01-02-15-04-05"
	sdate := startTime.Format(format)
	edate := endTime.Format(format)

	hours := endTime.Sub(startTime).Hours()
	if hours <= 0 {
		backend.Logger.Error("Invalid time range", "startDate", sdate, "endDate", edate)
		return nil, fmt.Errorf("invalid time range: start date %v must be before end date %v", startTime, endTime)
	}

	var avg string
	switch {
	case hours <= 24:
		avg = "0"
	case hours <= 48:
		avg = "60"
	case hours <= 72:
		avg = "300"
	case hours <= 168:
		avg = "900"
	case hours <= 336:
		avg = "1800"
	case hours <= 720:
		avg = "3600"
	case hours <= 1440:
		avg = "7200"
	case hours <= 2160:
		avg = "14400"
	default:
		avg = "86400"
	}

	backend.Logger.Info("Historical data parameters",
		"sensorID", sensorID,
		"startDate", sdate,
		"endDate", edate,
		"hours", hours,
		"avg", avg,
		"expectedDataPoints", hours*3600/float64(mustParseInt(avg, 1)))

	params := map[string]string{
		"id":         sensorID,
		"columns":    "datetime,value_",
		"avg":        avg,
		"sdate":      sdate,
		"edate":      edate,
		"count":      "50000",
		"usecaption": "1",
	}

	body, err := a.baseExecuteRequest("historicdata.json", params)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %w", err)
	}

	var response PrtgHistoricalDataResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	backend.Logger.Info("Historical data response received successfully")

	// Optional: Rohantwort in eine Datei schreiben (für Debugging)
	if err := os.WriteFile("historical_data_response.txt", body, 0644); err != nil {
		backend.Logger.Warn("Could not save historical data response to file", "error", err)
	}

	if len(response.HistData) == 0 {
		return nil, fmt.Errorf("no data found for the given time range")
	}
	backend.Logger.Info("First datetime in response", "datetime", response.HistData[0].Datetime)

	return &response, nil
	// 14.02.2025 13:49:00
}

// Yardımcı fonksiyon: string'i int'e çevirir, hata durumunda varsayılan değeri döner
func mustParseInt(s string, defaultVal int64) int64 {
	if s == "0" {
		return 60 // Ham veri için 1 dakikalık aralık varsayıyoruz
	}
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultVal
	}
	return val
}
