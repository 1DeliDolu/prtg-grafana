package plugin

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// PRTGDatasource repräsentiert das PRTG Datasource Plugin.
type PRTGDatasource struct {
	api PRTGAPI
}

// PRTGAPI definiert die Schnittstelle für API-Operationen.
type PRTGAPI interface {
	GetGroups() (*PrtgGroupListResponse, error)
	GetDevices() (*PrtgDevicesListResponse, error)
	GetSensors() (*PrtgSensorsListResponse, error)
	// Zusätzliche Methoden wie GetTextData, GetPropertyData etc. sollten hier deklariert werden.
}

func (d *PRTGDatasource) handlePropertyQuery(qm queryModel, filterProperty string) backend.DataResponse {
	var response backend.DataResponse
	if !isValidPropertyType(qm.Property) {
		return backend.ErrDataResponse(backend.StatusBadRequest, "Ungültiger Eigenschaftstyp")
	}
	if filterProperty == "" {
		filterProperty = "status"
	}
	frame := data.NewFrame("response")
	var fields []*data.Field

	switch qm.Property {
	case "group":
		groups, err := d.api.GetGroups()
		if err != nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("API Anfrage fehlgeschlagen: %v", err))
		}
		var groupInfo *PrtgGroupListItemStruct
		for _, g := range groups.Groups {
			if g.Group == qm.Group {
				groupInfo = &g
				break
			}
		}
		if groupInfo == nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, "Gruppe nicht gefunden")
		}
		timestamp, _, err := parsePRTGDateTime(groupInfo.Datetime)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("Datum parsen fehlgeschlagen: %v", err))
		}
		value := d.getPropertyValue(filterProperty, groupInfo)
		fields = d.createFields(timestamp, filterProperty, value, groupInfo.Group, "", "")

	case "device":
		devices, err := d.api.GetDevices()
		if err != nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("API Anfrage fehlgeschlagen: %v", err))
		}
		var deviceInfo *PrtgDeviceListItemStruct
		for _, d := range devices.Devices {
			if d.Device == qm.Device {
				deviceInfo = &d
				break
			}
		}
		if deviceInfo == nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, "Gerät nicht gefunden")
		}
		timestamp, _, err := parsePRTGDateTime(deviceInfo.Datetime)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("Datum parsen fehlgeschlagen: %v", err))
		}
		value := d.getPropertyValue(filterProperty, deviceInfo)
		fields = d.createFields(timestamp, filterProperty, value, deviceInfo.Group, deviceInfo.Device, "")

	case "sensor":
		sensors, err := d.api.GetSensors()
		if err != nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("API Anfrage fehlgeschlagen: %v", err))
		}
		var sensorInfo *PrtgSensorListItemStruct
		for _, s := range sensors.Sensors {
			if s.Sensor == qm.Sensor {
				sensorInfo = &s
				break
			}
		}
		if sensorInfo == nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, "Sensor nicht gefunden")
		}
		timestamp, _, err := parsePRTGDateTime(sensorInfo.Datetime)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("Datum parsen fehlgeschlagen: %v", err))
		}
		value := d.getPropertyValue(filterProperty, sensorInfo)
		fields = d.createFields(timestamp, filterProperty, value, sensorInfo.Group, sensorInfo.Device, sensorInfo.Sensor)

	default:
		backend.Logger.Warn("Unbekannter Eigenschaftstyp", "type", qm.Property)
		return backend.ErrDataResponse(backend.StatusBadRequest, "Unbekannter Eigenschaftstyp")
	}

	frame.Fields = fields
	response.Frames = append(response.Frames, frame)
	return response
}

func isValidPropertyType(propertyType string) bool {
	validTypes := map[string]bool{
		"group":  true,
		"device": true,
		"sensor": true,
	}
	return validTypes[propertyType]
}

func (d *PRTGDatasource) getPropertyValue(property string, item interface{}) string {
	v := reflect.ValueOf(item)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	switch property {
	case "status":
		if field := v.FieldByName("Status"); field.IsValid() {
			return field.String()
		}
	case "message":
		if field := v.FieldByName("Message"); field.IsValid() {
			return field.String()
		}
	case "active":
		if field := v.FieldByName("Active"); field.IsValid() {
			return strconv.FormatBool(field.Bool())
		}
	case "priority":
		if field := v.FieldByName("PriorityRAW"); field.IsValid() {
			return strconv.FormatInt(field.Int(), 10)
		}
	case "tags":
		if field := v.FieldByName("Tags"); field.IsValid() {
			return field.String()
		}
	}
	return "Unknown"
}

func (d *PRTGDatasource) createFields(timestamp time.Time, filterProperty, value, group, device, sensor string) []*data.Field {
	var nameParts []string
	if group != "" {
		nameParts = append(nameParts, group)
	}
	if device != "" {
		nameParts = append(nameParts, device)
	}
	if sensor != "" {
		nameParts = append(nameParts, sensor)
	}
	nameParts = append(nameParts, filterProperty)
	displayName := strings.Join(nameParts, " - ")

	return []*data.Field{
		data.NewField("Time", nil, []time.Time{timestamp}),
		data.NewField(filterProperty, nil, []string{value}).SetConfig(&data.FieldConfig{
			DisplayName: displayName,
		}),
	}
}
