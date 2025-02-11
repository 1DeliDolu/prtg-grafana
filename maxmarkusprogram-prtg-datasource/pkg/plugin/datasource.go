package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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

func (d *Datasource) query(_ context.Context, _ backend.PluginContext, query backend.DataQuery) backend.DataResponse {
	var response backend.DataResponse
	var qm queryModel

	// Unmarshal the JSON into our queryModel
	err := json.Unmarshal(query.JSON, &qm)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("json unmarshal: %v", err.Error()))
	}

	// Debug print
	fmt.Printf("Query Model: %+v\n", qm)
	fmt.Printf("Time Range: From=%v To=%v\n", query.TimeRange.From, query.TimeRange.To)

	// Create data frame
	frame := data.NewFrame("response")

	switch qm.QueryType {
	case "metrics":
		if qm.ObjectId == "" {
			return backend.ErrDataResponse(backend.StatusBadRequest, "missing objid parameter")
		}

		historicalData, err := d.api.GetHistoricalData(qm.ObjectId, query.TimeRange.From, query.TimeRange.To)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusInternal, fmt.Sprintf("error fetching historical data: %v", err))
		}

		// Create time and value slices
		times := make([]time.Time, 0, len(historicalData.HistData))
		values := make([]float64, 0, len(historicalData.HistData))

		// Process the historical data
		for _, data := range historicalData.HistData {
			// Parse PRTG's datetime format
			parsedTime, err := time.Parse("2006-01-02-15-04-05", data.Datetime)
			if err != nil {
				fmt.Printf("Warning: Failed to parse time '%s': %v\n", data.Datetime, err)
				continue
			}

			if value, ok := d.extractValue(data.Value, qm.Channel); ok {
				times = append(times, parsedTime)
				values = append(values, value)
				fmt.Printf("Added datapoint: time=%v value=%v\n", parsedTime, value)
			}
		}

		if len(times) == 0 {
			return backend.ErrDataResponse(backend.StatusInternal, "no valid data points found")
		}

		// Add fields to the frame
		frame.Fields = append(frame.Fields,
			data.NewField("time", nil, times),
			data.NewField("value", nil, values).SetConfig(&data.FieldConfig{
				DisplayName: d.createDisplayName(qm),
			}),
		)

	default:
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("unknown query type: %s", qm.QueryType))
	}

	response.Frames = append(response.Frames, frame)
	return response
}

// Add this helper function to try multiple time formats
func parseMultipleTimeFormats(timeStr string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-03-15-04-05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("could not parse time string: %s", timeStr)
}

func (d *Datasource) extractValue(values map[string]interface{}, channel string) (float64, bool) {
	if channel != "" && channel != "*" {
		// Look for specific channel
		for k, v := range values {
			if strings.EqualFold(k, channel) {
				if fVal, ok := v.(float64); ok {
					return fVal, true
				}
			}
		}
	} else {
		// Take first numeric value
		for _, v := range values {
			if fVal, ok := v.(float64); ok {
				return fVal, true
			}
		}
	}
	return 0, false
}

func (d *Datasource) createDisplayName(qm queryModel) string {
	parts := []string{}
	if qm.ObjectId != "" {
		parts = append(parts, fmt.Sprintf("ID: %s", qm.ObjectId))
	}
	if qm.IncludeGroupName && qm.Group != "" {
		parts = append(parts, qm.Group)
	}
	if qm.IncludeDeviceName && qm.Device != "" {
		parts = append(parts, qm.Device)
	}
	if qm.IncludeSensorName && qm.Sensor != "" {
		parts = append(parts, qm.Sensor)
	}
	if qm.Channel != "" {
		parts = append(parts, qm.Channel)
	}
	return strings.Join(parts, " - ")
}

// extractFilterProperties extracts the filter properties from the given data
func extractFilterProperties(data interface{}, _ string) []string {
	var filterProperties []string
	switch v := data.(type) {
	case []Group:
		filterProperties = make([]string, len(v))
		for i, item := range v {
			filterProperties[i] = item.Group
		}
	case []Device:
		filterProperties = make([]string, len(v))
		for i, item := range v {
			filterProperties[i] = item.Device
		}
	case []Sensor:
		filterProperties = make([]string, len(v))
		for i, item := range v {
			filterProperties[i] = item.Sensor
		}
	}
	return filterProperties
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
	case "historical":
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
		return d.handleGetHistorical(sender, pathParts[1])
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

func (d *Datasource) handleGetHistorical(sender backend.CallResourceResponseSender, objid string) error {
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

	historicalData, err := d.api.GetHistoricalData(objid, time.Now().Add(-24*time.Hour), time.Now())
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

	body, err := json.Marshal(historicalData)
	if err != nil {
		errorResponse := map[string]string{"error": fmt.Sprintf("error marshaling historical data: %v")}
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
