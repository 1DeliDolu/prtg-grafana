package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"os"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/maxmarkusprogram/prtg/pkg/models"
)

// Make sure Datasource implements required interfaces. This is important to do
// since otherwise we will only get a not implemented error response from plugin in
// runtime. In this example datasource instance implements backend.QueryDataHandler,
// backend.CheckHealthHandler interfaces. Plugin should not implement all these
// interfaces - only those which are required for a particular task.
var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
	_ backend.CallResourceHandler   = (*Datasource)(nil)
)

// NewDatasource creates a new datasource instance.
func NewDatasource(_ context.Context, settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	config, err := models.LoadPluginSettings(settings)
	if err != nil {
		return nil, err
	}

	baseURL := fmt.Sprintf("https://%s", config.Path)

	fmt.Println("baseURL: ", baseURL)

	// Default cache time if not set
	cacheTime := config.CacheTime
	if cacheTime <= 0 {
		cacheTime = 30 * time.Second // default cache time
	}

	return &Datasource{
		baseURL: baseURL,
		api:     NewApi(baseURL, config.Secrets.ApiKey, cacheTime, 10*time.Second),
	}, nil
}

// Dispose here tells plugin SDK that plugin wants to clean up resources when a new instance
// created. As soon as datasource settings change detected by SDK old datasource instance will
// be disposed and a new one will be created using NewSampleDatasource factory function.
func (d *Datasource) Dispose() {
	// Clean up datasource instance resources.
}

// QueryData handles multiple queries and returns multiple responses.
// req contains the queries []DataQuery (where each query contains RefID as a unique identifier).
// The QueryDataResponse contains a map of RefID to the response for each query, and each response
// contains Frames ([]*Frame).
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	// create response struct
	response := backend.NewQueryDataResponse()

	// loop over queries and execute them individually.
	for _, q := range req.Queries {
		res := d.query(ctx, req.PluginContext, q)

		// save the response in a hashmap
		// based on with RefID as identifier
		response.Responses[q.RefID] = res
	}

	return response, nil
}
// parsePRTGDateTime parses PRTG datetime format "DD.MM.YYYY HH:mm:ss" and converts to "YYYY-MM-DD HH:mm:ss"
func parsePRTGDateTime(datetime string) (string, error) {
	// Create or open log file
	file, err := os.OpenFile("datetime_log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to open log file: %v", err)
	}
	defer file.Close()

	// Split date and time parts
	parts := strings.Split(datetime, " ")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid datetime format: %s", datetime)
	}

	datePart := parts[0]
	timePart := parts[1]

	// Parse date (DD.MM.YYYY)
	dateParts := strings.Split(datePart, ".")
	if len(dateParts) != 3 {
		return "", fmt.Errorf("invalid date format: %s", datePart)
	}

	// Convert to YYYY-MM-DD format
	formattedDate := fmt.Sprintf("%s-%s-%s", dateParts[2], dateParts[1], dateParts[0])
	formattedDateTime := formattedDate + " " + timePart

	// Log the conversion
	logEntry := fmt.Sprintf("Original: %s -> Converted: %s\n", datetime, formattedDateTime)
	if _, err := file.WriteString(logEntry); err != nil {
		fmt.Printf("Error writing to log: %v\n", err)
	}

	return formattedDateTime, nil
}

func (d *Datasource) query(_ context.Context, _ backend.PluginContext, query backend.DataQuery) backend.DataResponse {
	var response backend.DataResponse
	var qm queryModel

	err := json.Unmarshal(query.JSON, &qm)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("json unmarshal: %v", err.Error()))
	}

	// Check required parameters
	if qm.ObjectId == "" || qm.Channel == "" {
		return response
	}

	// Convert timestamps to Unix time in seconds
	fromTime := query.TimeRange.From.Unix()
	toTime := query.TimeRange.To.Unix()

	// Get historical data with the Unix timestamps
	historicalData, err := d.api.GetHistoricalData(qm.ObjectId, fromTime, toTime)
	if err != nil {
		response.Error = err
		return response
	}

	// Check if we have any data
	if len(historicalData.HistData) == 0 {
		response.Error = fmt.Errorf("no data found for sensor")
		return response
	}

	// Create frame
	frame := data.NewFrame("response")

	// Extract the requested parameter values based on datetime and value
	times := make([]time.Time, len(historicalData.HistData))
	values := make([]float64, len(historicalData.HistData))

	// Open file for logging first 5 time values
	file, err := os.OpenFile("time_values.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		defer file.Close()
		file.WriteString("\n--- New Query Results ---\n")
	}

	// Process data points
	for i := range historicalData.HistData {
		// Parse date in PRTG format using our custom parser
		formattedDateTime, err := parsePRTGDateTime(historicalData.HistData[i].Datetime)
		if err != nil {
			fmt.Printf("Warning: Failed to parse time '%s': %v\n", historicalData.HistData[i].Datetime, err)
			continue
		}

		// Convert the formatted datetime string to time.Time
		t, err := time.Parse("2006-01-02 15:04:05", formattedDateTime)
		if err != nil {
			fmt.Printf("Warning: Failed to parse formatted time '%s': %v\n", formattedDateTime, err)
			continue
		}

		// Log first 5 time values to file
		if i < 5 && file != nil {
			fmt.Fprintf(file, "Time %d: %s\n", i+1, t.Format("2006-01-02 15:04:05"))
		}

		if val, ok := historicalData.HistData[i].Value[qm.Channel]; ok {
			if floatVal, ok := val.(float64); ok {
				times[i] = t
				values[i] = floatVal
			}
		}
	}

	// Build display name based on user preferences
	displayName := qm.Channel
	if qm.IncludeGroupName || qm.IncludeDeviceName || qm.IncludeSensorName {
		parts := []string{}
		if qm.IncludeGroupName && qm.Group != "" {
			parts = append(parts, qm.Group)
		}
		if qm.IncludeDeviceName && qm.Device != "" {
			parts = append(parts, qm.Device)
		}
		if qm.IncludeSensorName && qm.Sensor != "" {
			parts = append(parts, qm.Sensor)
		}
		parts = append(parts, qm.Channel)
		displayName = strings.Join(parts, " - ")
	}

	// Add fields to frame with proper configuration
	frame.Fields = append(frame.Fields,
		data.NewField("Time", nil, times),
		data.NewField("Value", nil, values).SetConfig(&data.FieldConfig{
			DisplayName: displayName,
		}),
	)

	response.Frames = append(response.Frames, frame)
	return response
}

/* ########################################## CHECK HEALTH  ############################################ */

// CheckHealth handles health checks sent from Grafana to the plugin.
// The main use case for these health checks is the test button on the
// datasource configuration page which allows users to verify that
// a datasource is working as expected.
func (d *Datasource) CheckHealth(_ context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	res := &backend.CheckHealthResult{}
	config, err := models.LoadPluginSettings(*req.PluginContext.DataSourceInstanceSettings)

	if err != nil {
		res.Status = backend.HealthStatusError
		res.Message = "Unable to load settings"
		return res, nil
	}

	if config.Secrets.ApiKey == "" {
		res.Status = backend.HealthStatusError
		res.Message = "API key is missing"
		return res, nil
	}

	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "Data source is working",
	}, nil
}

/* ########################################## CALL RESOURCE   ############################################ */

func (d *Datasource) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
	// Extract the path parts
	pathParts := strings.Split(req.Path, "/")

	switch pathParts[0] {
	case "groups":
		return d.handleGetGroups(sender)
	case "devices":
		return d.handleGetDevices(sender)
	case "sensors":
		return d.handleGetSensors(sender)
	case "channels":
		// Check if we have an objid in the path
		if len(pathParts) < 2 {
			errorResponse := map[string]string{"error": "missing objid parameter"}
			errorJSON, _ := json.Marshal(errorResponse)
			return sender.Send(&backend.CallResourceResponse{
				Status: http.StatusBadRequest,
				Headers: map[string][]string{
					"Content-Type": {"application/json"},
				},
				Body: errorJSON,
			})
		}
		return d.handleGetChannel(sender, pathParts[1])
	default:
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusNotFound,
		})
	}
}

func (d *Datasource) handleGetGroups(sender backend.CallResourceResponseSender) error {
	groups, err := d.api.GetGroups()
	if err != nil {
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusInternalServerError,
			Body:   []byte(err.Error()),
		})
	}

	body, err := json.Marshal(groups)
	if err != nil {
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusInternalServerError,
			Body:   []byte(fmt.Sprintf("error marshaling groups: %v", err)),
		})
	}

	return sender.Send(&backend.CallResourceResponse{
		Status: http.StatusOK,
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
		Body: body,
	})
}

func (d *Datasource) handleGetDevices(sender backend.CallResourceResponseSender) error {
	devices, err := d.api.GetDevices()
	if err != nil {
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusInternalServerError,
			Body:   []byte(err.Error()),
		})
	}

	body, err := json.Marshal(devices)
	if err != nil {
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusInternalServerError,
			Body:   []byte(fmt.Sprintf("error marshaling devices: %v", err)),
		})
	}

	return sender.Send(&backend.CallResourceResponse{
		Status: http.StatusOK,
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
		Body: body,
	})
}

func (d *Datasource) handleGetSensors(sender backend.CallResourceResponseSender) error {
	sensors, err := d.api.GetSensors()
	if err != nil {
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusInternalServerError,
			Body:   []byte(err.Error()),
		})
	}

	body, err := json.Marshal(sensors)
	if err != nil {
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusInternalServerError,
			Body:   []byte(fmt.Sprintf("error marshaling sensors: %v", err)),
		})
	}

	return sender.Send(&backend.CallResourceResponse{
		Status: http.StatusOK,
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
		Body: body,
	})
}

func (d *Datasource) handleGetChannel(sender backend.CallResourceResponseSender, objid string) error {
	if objid == "" {
		errorResponse := map[string]string{"error": "missing objid parameter"}
		errorJSON, _ := json.Marshal(errorResponse)
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusBadRequest,
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			Body: errorJSON,
		})
	}

	channels, err := d.api.GetChannels(objid)
	if err != nil {
		errorResponse := map[string]string{"error": err.Error()}
		errorJSON, _ := json.Marshal(errorResponse)
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusInternalServerError,
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			Body: errorJSON,
		})
	}

	body, err := json.Marshal(channels)
	if err != nil {
		errorResponse := map[string]string{"error": fmt.Sprintf("error marshaling channels: %v", err)}
		errorJSON, _ := json.Marshal(errorResponse)
		return sender.Send(&backend.CallResourceResponse{
			Status: http.StatusInternalServerError,
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			Body: errorJSON,
		})
	}

	return sender.Send(&backend.CallResourceResponse{
		Status: http.StatusOK,
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
		Body: body,
	})
}


