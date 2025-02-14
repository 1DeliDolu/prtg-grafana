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

// Aşağıdaki satırlarla Datasource, gerekli Grafana SDK arayüzlerini implemente ettiğinden emin oluyoruz.
var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
	_ backend.CallResourceHandler   = (*Datasource)(nil)
)

// NewDatasource, plugin ayarlarından verileri çekerek yeni bir datasource örneği oluşturur.
func NewDatasource(ctx context.Context, settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	config, err := models.LoadPluginSettings(settings)
	if err != nil {
		return nil, err
	}
	baseURL := fmt.Sprintf("https://%s", config.Path)
	backend.Logger.Info("Base URL", "url", baseURL)

	// Eğer cache zamanı tanımlı değilse varsayılan 30 saniye kullanılır.
	cacheTime := config.CacheTime
	if cacheTime <= 0 {
		cacheTime = 30 * time.Second
	}

	return &Datasource{
		baseURL: baseURL,
		api:     NewApi(baseURL, config.Secrets.ApiKey, cacheTime, 10*time.Second),
	}, nil
}

// Dispose, datasource ayarları değiştiğinde çağrılır.
func (d *Datasource) Dispose() {
	// Gerekirse kaynak temizleme işlemleri yapılabilir.
}

// QueryData, gelen sorguları işler ve sonuçları döner.
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	response := backend.NewQueryDataResponse()

	// Her sorgu için query metodunu çağırıyoruz.
	for _, q := range req.Queries {
		res := d.query(ctx, req.PluginContext, q)
		response.Responses[q.RefID] = res
	}

	return response, nil
}

// parsePRTGDateTime parses PRTG datetime strings in various formats
func parsePRTGDateTime(datetime string) (time.Time, string, error) {
	// Try different known PRTG date formats
	layouts := []string{
		"02.01.2006 15:04:05",
		time.RFC3339,
	}

	var parseErr error
	for _, layout := range layouts {
		parsedTime, err := time.Parse(layout, datetime)
		if err == nil {
			unixTime := parsedTime.Unix()
			return parsedTime, strconv.FormatInt(unixTime, 10), nil
		}
		parseErr = err
	}

	backend.Logger.Error("Date parsing failed for all formats",
		"datetime", datetime,
		"error", parseErr)
	return time.Time{}, "", fmt.Errorf("failed to parse time '%s': %w", datetime, parseErr)
}

// query, tek bir sorguyu işler. Eğer QueryType "metrics" ise zaman serisi oluşturur,
// aksi halde property bazlı sorgular handlePropertyQuery ile işlenir.
func (d *Datasource) query(ctx context.Context, pCtx backend.PluginContext, query backend.DataQuery) backend.DataResponse {
	var response backend.DataResponse
	var qm queryModel

	backend.Logger.Debug("Raw query parameters",
		"timeRange", fmt.Sprintf("%v to %v", query.TimeRange.From, query.TimeRange.To),
		"rawJSON", string(query.JSON))
	if err := json.Unmarshal(query.JSON, &qm); err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("JSON unmarshal error: %v", err))
	}

	switch qm.QueryType {
	case "metrics":
		// Existing metrics handling code
		fromTime := query.TimeRange.From.UnixMilli()
		toTime := query.TimeRange.To.UnixMilli()

		backend.Logger.Info("Fetching historical data",
			"objectId", qm.ObjectId,
			"channel", qm.Channel,
			"from", fromTime,
			"to", toTime)
		historicalData, err := d.api.GetHistoricalData(qm.ObjectId, fromTime, toTime)
		if err != nil {
			backend.Logger.Error("API request failed", "error", err)
			return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("API request failed: %v", err))
		}
		backend.Logger.Info("Received historical data", "dataPoints", len(historicalData.HistData))

		// Annahme: historicalData.Treesize enthält den Wert aus dem JSON ("treesize")
		times := make([]time.Time, 0,len(historicalData.HistData))
		values := make([]float64, 0, len(historicalData.HistData))

		backend.Logger.Debug("Parsing historical data", "channel", len(times))

		for _, item := range historicalData.HistData {
			parsedTime, _, err := parsePRTGDateTime(item.Datetime)
			if err != nil {
				backend.Logger.Warn("Date parsing failed", "datetime", item.Datetime, "error", err)
				continue
			}
			if val, ok := item.Value[qm.Channel]; ok {
				switch v := val.(type) {
				case float64:
					values = append(values, v)
				case string:
					if floatVal, err := strconv.ParseFloat(v, 64); err == nil {
						values = append(values, floatVal)
					} else {
						backend.Logger.Warn("Cannot convert value to float64", "value", v, "error", err)
						continue
					}
				default:
					backend.Logger.Warn("Unexpected value type", "type", fmt.Sprintf("%T", v), "value", v)
					continue
				}
				times = append(times, parsedTime)
			} else {
				backend.Logger.Warn("Channel not found in item.Value, using default value", "channel", qm.Channel)
				times = append(times, parsedTime)
				values = append(values, 0.0)
			}
		}

		var parts []string
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
		displayName := strings.Join(parts, " - ")

		frame := data.NewFrame("response",
			data.NewField("Time", nil, times),
			data.NewField("Value", nil, values).SetConfig(&data.FieldConfig{
				DisplayName: displayName,
			}),
		)

		response.Frames = append(response.Frames, frame)

	case "text":
		// Handle text mode by using the non-raw property
		return d.handlePropertyQuery(qm, qm.FilterProperty)

	case "raw":
		// Handle raw mode by appending "_raw" to the filter property
		rawProperty := qm.FilterProperty
		if !strings.HasSuffix(rawProperty, "_raw") {
			rawProperty += "_raw"
		}
		return d.handlePropertyQuery(qm, rawProperty)

	default:
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("Unknown query type: %s", qm.QueryType))
	}

	return response
}

// CheckHealth, plugin konfigürasyonunu kontrol eder.
func (d *Datasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	res := &backend.CheckHealthResult{}

	// Load configuration
	config, err := models.LoadPluginSettings(*req.PluginContext.DataSourceInstanceSettings)
	if err != nil {
		res.Status = backend.HealthStatusError
		res.Message = "Unable to load settings"
		return res, nil
	}

	// Check API key
	if config.Secrets.ApiKey == "" {
		res.Status = backend.HealthStatusError
		res.Message = "API key is missing"
		return res, nil
	}

	// Get PRTG status including version
	status, err := d.api.GetStatusList()
	if err != nil {
		res.Status = backend.HealthStatusError
		res.Message = fmt.Sprintf("Failed to get PRTG status: %v", err)
		return res, nil
	}

	// Return success with version information
	res.Status = backend.HealthStatusOk
	res.Message = fmt.Sprintf("Data source is working. PRTG Version: %s", status.Version)
	return res, nil
}

// CallResource, URL path'ine göre istekleri ilgili handler'lara yönlendirir.
func (d *Datasource) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
	pathParts := strings.Split(req.Path, "/")
	switch pathParts[0] {
	case "groups":
		return d.handleGetGroups(sender)
	case "devices":
		return d.handleGetDevices(sender)
	case "sensors":
		return d.handleGetSensors(sender)
	case "channels":
		if len(pathParts) < 2 {
			errorResponse := map[string]string{"error": "missing objid parameter"}
			errorJSON, _ := json.Marshal(errorResponse)
			return sender.Send(&backend.CallResourceResponse{
				Status:  http.StatusBadRequest,
				Headers: map[string][]string{"Content-Type": {"application/json"}},
				Body:    errorJSON,
			})
		}
		return d.handleGetChannel(sender, pathParts[1])
	default:
		return sender.Send(&backend.CallResourceResponse{Status: http.StatusNotFound})
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
		Status:  http.StatusOK,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    body,
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
		Status:  http.StatusOK,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    body,
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
		Status:  http.StatusOK,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    body,
	})
}

func (d *Datasource) handleGetChannel(sender backend.CallResourceResponseSender, objid string) error {
	if objid == "" {
		errorResponse := map[string]string{"error": "missing objid parameter"}
		errorJSON, _ := json.Marshal(errorResponse)
		return sender.Send(&backend.CallResourceResponse{
			Status:  http.StatusBadRequest,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    errorJSON,
		})
	}
	channels, err := d.api.GetChannels(objid)
	if err != nil {
		errorResponse := map[string]string{"error": err.Error()}
		errorJSON, _ := json.Marshal(errorResponse)
		return sender.Send(&backend.CallResourceResponse{
			Status:  http.StatusInternalServerError,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    errorJSON,
		})
	}
	body, err := json.Marshal(channels)
	if err != nil {
		errorResponse := map[string]string{"error": fmt.Sprintf("error marshaling channels: %v", err)}
		errorJSON, _ := json.Marshal(errorResponse)
		return sender.Send(&backend.CallResourceResponse{
			Status:  http.StatusInternalServerError,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    errorJSON,
		})
	}
	return sender.Send(&backend.CallResourceResponse{
		Status:  http.StatusOK,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    body,
	})
	// 14.02.2025 13:49:00
}
