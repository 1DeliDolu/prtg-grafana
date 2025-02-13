package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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

// parsePRTGDateTime parses PRTG datetime format "DD.MM.YYYY HH:mm:ss" and returns time.Time
func parsePRTGDateTime(datetime string) (time.Time, string, error) {
	// PRTG Tarih formatı: "DD.MM.YYYY HH:mm:ss"
	layout := "02.01.2006 15:04:05"

	// Parse datetime string using the correct format
	parsedTime, err := time.Parse(layout, datetime)
	if err != nil {
		backend.Logger.Warn("Date parsing failed", "datetime", datetime, "error", err)
		return time.Time{}, "", fmt.Errorf("failed to parse time: %w", err)
	}

	// Convert to Unix timestamp
	unixTime := parsedTime.Unix()
	formattedTime := strconv.FormatInt(unixTime, 10)

	

	// Log çıktısı
	backend.Logger.Info("Time conversion", 
		"original", datetime,
		"unix", unixTime)

	return parsedTime, formattedTime, nil
}

func (d *Datasource) query(_ context.Context, _ backend.PluginContext, query backend.DataQuery) backend.DataResponse {
	var response backend.DataResponse
	var qm queryModel

	backend.Logger.Debug("Raw query parameters",
		"timeRange", fmt.Sprintf("%v to %v", query.TimeRange.From, query.TimeRange.To),
		"rawJSON", string(query.JSON))

	if err := json.Unmarshal(query.JSON, &qm); err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("json unmarshal error: %v", err))
	}

	fromTime := query.TimeRange.From.UnixMilli()
	toTime := query.TimeRange.To.UnixMilli()

	backend.Logger.Info("Fetching historical data",
		"objectId", qm.ObjectId,
		"channel", qm.Channel,
		"from", fromTime,
		"to", toTime)

	
	if qm.QueryType == "metrics" {
	
	historicalData, err := d.api.GetHistoricalData(qm.ObjectId, fromTime, toTime)
	if err != nil {
		backend.Logger.Error("API request failed", "error", err)
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("API request failed: %v", err))
	}

	backend.Logger.Info("Received historical data", "dataPoints", len(historicalData.HistData))

	// Burada slice'ları sabit uzunlukta başlatıyoruz!
	times := make([]time.Time, len(historicalData.HistData))
	values := make([]float64, len(historicalData.HistData))

	for i, item := range historicalData.HistData {
		formattedDateTime, _, err := parsePRTGDateTime(item.Datetime)
		if err != nil {
			backend.Logger.Warn("Date parsing failed", "datetime", item.Datetime, "error", err)
			continue
		}

		backend.Logger.Info("Formatted Time", "formattedDateTime", formattedDateTime)
		backend.Logger.Info("Channel", "channel", qm.Channel)

		backend.Logger.Info("Item Value", "item.Value", item.Value, "qm.Channel", qm.Channel, "item.Value[qm.Channel]", item.Value[qm.Channel])

		if val, ok := item.Value[qm.Channel]; ok {
			switch v := val.(type) {
			case float64:
				values[i] = v
				backend.Logger.Info("Value float64", "value", v)
			case string:
				if floatVal, err := strconv.ParseFloat(v, 64); err == nil {
					values[i] = floatVal
				} else {
					backend.Logger.Warn("Cannot convert value to float64", "value", v, "error", err)
					continue
				}
			default:
				backend.Logger.Warn("Unexpected value type", "type", fmt.Sprintf("%T", v), "value", v)
				continue
			}
			times[i] = formattedDateTime  // ❗ Artık index hatası vermeyecek
		} else {
			// Eğer JSON'da değer yoksa, varsayılan değeri 0.0 olarak ayarla
			backend.Logger.Warn("Channel not found in item.Value, setting default value", "channel", qm.Channel)
			times[i] = formattedDateTime  // ❗ Eğer veri yoksa bile tarih atanmalı
			values[i] = 0.0               // ❗ Varsayılan değer olarak 0 atanmalı
		}
	}
	backend.Logger.Info("Fetching historical data",
		"objectId", qm.ObjectId,
		"channel", qm.Channel,
		"from", fromTime,
		"to", toTime)


	backend.Logger.Info("Processed data points", "totalCount", len(historicalData.HistData))

	if len(times) > 0 && len(values) > 0 {
		backend.Logger.Info("Processed data points",
			"totalCount", len(historicalData.HistData),
			"firstTimestamp", times[0],
			"firstValue", values[0])
	} else {
		backend.Logger.Warn("No valid data points were processed")
	}

	var displayName string
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

	frame := data.NewFrame("response")

	frame.Fields = append(frame.Fields,
		data.NewField("Time", nil, times),
		data.NewField("Value", nil, values).SetConfig(&data.FieldConfig{
			DisplayName: displayName,
		}),
	)

	response.Frames = append(response.Frames, frame)
	return response
	} else if qm.QueryType == "text" {
	textData, err := d.api.GetTextData(qm.ObjectId, fromTime, toTime)
	if err != nil {
		backend.Logger.Error("API request failed", "error", err)
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("API request failed: %v", err))
	}

	return d.handlePropertyQuery(qm, qm.Property)
	}else if qm.QueryType == "raw" {

	rawData, err := d.api.GetPropertyData(qm.ObjectId, fromTime, toTime)
	if err != nil {
		backend.Logger.Error("API request failed", "error", err)
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("API request failed: %v", err))
	}

	return d.handlePropertyQuery(qm, qm.Property)
}



/* ########################################## CHECK HEALTH  ############################################ */

// CheckHealth handles health checks sent from Grafana to the plugin.
// The main use case for these health checks is the test button on the
// datasource configuration page which allows users to verify that
// a datasource is working as expected.
func (d *Datasource) CheckHealth(_ context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	config, err := models.LoadPluginSettings(req.PluginContext.DataSourceInstanceSettings)
	res := &backend.CheckHealthResult{}

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
