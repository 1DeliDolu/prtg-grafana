package plugin

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// Api speichert API-bezogene Konfigurationen und einen wiederverwendbaren HTTP-Client.
type Api struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewApi erstellt eine neue Api-Instanz mit dem angegebenen Request-Timeout.
func NewApi(baseURL, apiKey string, requestTimeout time.Duration) *Api {
	client := &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			// Achtung: InsecureSkipVerify sollte nur bei selbstsignierten Zertifikaten verwendet werden!
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	return &Api{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  client,
	}
}

// buildApiUrl erstellt eine standardisierte PRTG API URL.
func (a *Api) buildApiUrl(method string, params map[string]string) (string, error) {
	baseURL := fmt.Sprintf("%s/api/%s", a.baseURL, method)
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("ungültige URL: %w", err)
	}
	q := url.Values{}
	q.Set("apitoken", a.apiKey)
	for key, value := range params {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// baseExecuteRequest übernimmt die gemeinsame HTTP-Request-Logik.
func (a *Api) baseExecuteRequest(endpoint string, params map[string]string) ([]byte, error) {
	apiURL, err := a.buildApiUrl(endpoint, params)
	if err != nil {
		return nil, fmt.Errorf("URL-Erstellung fehlgeschlagen: %w", err)
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("Fehler beim Erstellen der Anfrage: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Anfrage fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		log.DefaultLogger.Error("Zugriff verweigert: Bitte API-Token und Berechtigungen überprüfen")
		return nil, fmt.Errorf("Zugriff verweigert: Bitte API-Token und Berechtigungen überprüfen")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unerwarteter Statuscode: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Fehler beim Lesen des Response-Bodys: %w", err)
	}
	backend.Logger.Debug("Roher Response-Body", "body", string(body))
	return body, nil
}

func (a *Api) GetStatusList() (*PrtgStatusListResponse, error) {
	body, err := a.baseExecuteRequest("status.json", nil)
	if err != nil {
		return nil, err
	}
	var response PrtgStatusListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("Fehler beim Parsen des Response: %w", err)
	}
	return &response, nil
}

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
		return nil, fmt.Errorf("Fehler beim Parsen des Response: %w", err)
	}
	return &response, nil
}

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
		return nil, fmt.Errorf("Fehler beim Parsen des Response: %w", err)
	}
	return &response, nil
}

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
	backend.Logger.Debug("Sensors raw response", "body", string(body))
	var response PrtgSensorsListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("Fehler beim Parsen des Response: %w", err)
	}
	return &response, nil
}

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
	// Optional: Speichere rohen Response zum Debuggen.
	if err := os.WriteFile("channel_response.txt", body, 0644); err != nil {
		backend.Logger.Warn("Response konnte nicht in Datei gespeichert werden", "error", err)
	}
	var response PrtgChannelValueStruct
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("Fehler beim Parsen des Response: %w", err)
	}
	return &response, nil
}

// GetHistoricalData ruft historische Daten für den angegebenen Sensor und Zeitraum ab.
func (a *Api) GetHistoricalData(sensorID string, startDate, endDate int64) (*PrtgHistoricalDataResponse, error) {
	backend.Logger.Info("GetHistoricalData aufgerufen", "sensorID", sensorID, "startDate", startDate, "endDate", endDate)
	if sensorID == "" {
		return nil, fmt.Errorf("ungültige Anfrage: Sensor-ID fehlt")
	}
	startTime := time.UnixMilli(startDate)
	endTime := time.UnixMilli(endDate)
	const format = "2006-01-02-15-04-05"
	sdate := startTime.Format(format)
	edate := endTime.Format(format)
	if !startTime.Before(endTime) {
		backend.Logger.Error("Ungültiger Zeitraum", "startTime", startTime, "endTime", endTime)
		return nil, fmt.Errorf("ungültiger Zeitraum: Startzeit %v muss vor Endzeit %v liegen", startTime, endTime)
	}
	hours := endTime.Sub(startTime).Hours()
	var avg string
	switch {
	case hours > 745:
		avg = "86400"
	case hours > 36:
		avg = "3600"
	case hours > 12:
		avg = "300"
	default:
		avg = "0"
	}
	params := map[string]string{
		"id":         sensorID,
		"avg":        avg,
		"sdate":      sdate,
		"edate":      edate,
		"count":      "50000",
		"usecaption": "1",
		"columns":    "datetime,value_",
	}
	body, err := a.baseExecuteRequest("historicdata.json", params)
	if err != nil {
		return nil, fmt.Errorf("historische Daten konnten nicht abgerufen werden: %w", err)
	}
	var response PrtgHistoricalDataResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("Fehler beim Parsen des Response: %w", err)
	}
	backend.Logger.Info("Historische Daten erfolgreich empfangen")
	if err := os.WriteFile("historical_data_response.txt", body, 0644); err != nil {
		backend.Logger.Warn("Response konnte nicht in Datei gespeichert werden", "error", err)
	}
	if len(response.HistData) == 0 {
		return nil, fmt.Errorf("keine Daten für den angegebenen Zeitraum gefunden")
	}
	backend.Logger.Info("Erster Zeitstempel in Response", "datetime", response.HistData[0].Datetime)
	return &response, nil
}
