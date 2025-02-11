package plugin

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Api struct to hold API related configurations
type Api struct {
	baseURL string
	apiKey  string
	timeout time.Duration
}

// NewApi creates a new Api instance
func NewApi(baseURL, apiKey string, timeout, requestTimeout time.Duration) *Api {
	return &Api{
		baseURL: baseURL,
		apiKey:  apiKey,
		timeout: requestTimeout,
	}
}

// buildApiUrl creates a standardized PRTG API URL
func (a *Api) buildApiUrl(method string, params map[string]string) (string, error) {
	baseUrl := fmt.Sprintf("%s/api/%s", a.baseURL, method)
	u, err := url.Parse(baseUrl)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	// Add query parameters
	q := url.Values{}
	q.Set("apitoken", a.apiKey)

	// Add additional parameters
	for key, value := range params {
		q.Set(key, value)
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}

// SetTimeout sets the API request timeout
func (a *Api) SetTimeout(timeout time.Duration) {
	if timeout > 0 {
		a.timeout = timeout
	}
}

// baseExecuteRequest handles the common HTTP request logic
func (a *Api) baseExecuteRequest(endpoint string, params map[string]string) ([]byte, error) {
	apiUrl, err := a.buildApiUrl(endpoint, params)
	if err != nil {
		return nil, fmt.Errorf("failed to build URL: %w", err)
	}

	// Disable TLS verification (for self-signed certificates)
	client := &http.Client{
		Timeout: a.timeout,
		Transport: &http.Transport{
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
		return nil, fmt.Errorf("access denied: please verify API token and permissions")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Log the raw response body for debugging
	fmt.Printf("Raw response body: %s\n", string(body))

	return body, nil
}

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

	var response PrtgSensorsListResponse
	fmt.Printf("Sensor Response: %s\n", string(body)) // Add this line to print raw response
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
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

	// Save raw response to file for debugging
	if err := os.WriteFile("channel_response.txt", body, 0644); err != nil {
		fmt.Printf("Warning: Could not save response to file: %v\n", err)
	}

	var response PrtgChannelValueStruct
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil

}

// GetHistoricalData retrieves historical data for the given sensor ID and time range
func (a *Api) GetHistoricalData(sensorID string, startDate, endDate time.Time, channel string) (*PrtgHistoricalDataResponse, error) {
	if sensorID == "" {
		return nil, fmt.Errorf("invalid query: missing sensor ID")
	}

	// Format dates and calculate interval
	sdate := startDate.Format("2006-01-02-15-04-05")
	edate := endDate.Format("2006-01-02-15-04-05")
	hours := endDate.Sub(startDate).Hours()

	// Determine averaging interval
	var avg string
	switch {
	case hours > 12 && hours < 36:
		avg = "300"
	case hours > 36 && hours < 745:
		avg = "3600"
	case hours > 745:
		avg = "86400"
	default:
		avg = "0"
	}

	// Build parameters
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
		return nil, fmt.Errorf("failed to fetch historical data: %w", err)
	}

	// Debug raw response
	fmt.Printf("Raw historical data response: %s\n", string(body))

	var response PrtgHistoricalDataResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse historical data response: %w", err)
	}

	// If no channel specified, return all data
	if channel == "" {
		return &response, nil
	}

	// Filter data for specific channel
	filteredData := make([]PrtgValues, 0)
	for _, data := range response.HistData {
		// Debug print values
		fmt.Printf("Processing data point: datetime=%s, values=%+v\n", data.Datetime, data.Value)

		// Try both exact and case-insensitive match
		value, found := data.Value[channel]
		if !found {
			// Try case-insensitive search
			lowerChannel := strings.ToLower(channel)
			for k, v := range data.Value {
				if strings.ToLower(k) == lowerChannel {
					value = v
					found = true
					break
				}
			}
		}

		if found {
			filteredData = append(filteredData, PrtgValues{
				Datetime: data.Datetime,
				Value:    map[string]interface{}{channel: value},
			})
		}
	}

	// Debug print filtered data
	fmt.Printf("Filtered data points: %d\n", len(filteredData))

	if len(filteredData) == 0 {
		// Return empty response instead of error
		return &PrtgHistoricalDataResponse{
			PrtgVersion: response.PrtgVersion,
			TreeSize:    response.TreeSize,
			HistData:    []PrtgValues{},
		}, nil
	}

	return &PrtgHistoricalDataResponse{
		PrtgVersion: response.PrtgVersion,
		TreeSize:    response.TreeSize,
		HistData:    filteredData,
	}, nil
}
