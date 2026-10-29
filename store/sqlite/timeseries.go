package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/apiv1"
)

// TimeSeries shares its owning Devices store's connection and lifetime.
type TimeSeries struct{ db *sql.DB }

var _ apiv1.TimeSeriesStore = (*TimeSeries)(nil)

func (s *Devices) TimeSeries() *TimeSeries { return &TimeSeries{db: s.db} }

func seriesPath(path string) (string, error) {
	p, err := lwm2m.ParsePath(path)
	if err != nil || (!p.IsResource() && !p.IsResourceInstance()) {
		return "", fmt.Errorf("invalid resource path")
	}
	return p.String(), nil
}

var seriesTypes = map[string]reflect.Type{
	"bool": reflect.TypeOf(false), "int": reflect.TypeOf(int(0)), "int8": reflect.TypeOf(int8(0)), "int16": reflect.TypeOf(int16(0)), "int32": reflect.TypeOf(int32(0)), "int64": reflect.TypeOf(int64(0)),
	"uint": reflect.TypeOf(uint(0)), "uint8": reflect.TypeOf(uint8(0)), "uint16": reflect.TypeOf(uint16(0)), "uint32": reflect.TypeOf(uint32(0)), "uint64": reflect.TypeOf(uint64(0)), "float32": reflect.TypeOf(float32(0)), "float64": reflect.TypeOf(float64(0)),
}

func (s *TimeSeries) Append(tenant, endpoint, path string, t time.Time, value any) error {
	path, err := seriesPath(path)
	if err != nil {
		return err
	}
	typ := reflect.TypeOf(value)
	if typ == nil || seriesTypes[typ.String()] != typ {
		return fmt.Errorf("value must be numeric or boolean")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("invalid value: %w", err)
	}
	_, err = s.db.Exec("INSERT INTO timeseries_points(tenant,endpoint,path,seconds,nanos,value_type,value) VALUES(?,?,?,?,?,?,?)", tenant, endpoint, path, t.Unix(), t.Nanosecond(), typ.String(), string(data))
	return err
}

func (s *TimeSeries) Query(tenant, endpoint, path string, from, to time.Time) ([]apiv1.TimeSeriesPoint, error) {
	path, err := seriesPath(path)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT seconds,nanos,value_type,value FROM timeseries_points
 WHERE tenant=? AND endpoint=? AND path=? AND (seconds,nanos)>=(?,?) AND (seconds,nanos)<=(?,?)
 ORDER BY seconds,nanos,sequence`, tenant, endpoint, path, from.Unix(), from.Nanosecond(), to.Unix(), to.Nanosecond())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	points := []apiv1.TimeSeriesPoint{}
	for rows.Next() {
		var seconds int64
		var nanos int
		var kind, raw string
		if err = rows.Scan(&seconds, &nanos, &kind, &raw); err != nil {
			return nil, err
		}
		typ, ok := seriesTypes[kind]
		if !ok {
			return nil, fmt.Errorf("unknown time series value type %q", kind)
		}
		value := reflect.New(typ)
		if err = json.Unmarshal([]byte(raw), value.Interface()); err != nil {
			return nil, fmt.Errorf("invalid time series value: %w", err)
		}
		points = append(points, apiv1.TimeSeriesPoint{T: time.Unix(seconds, int64(nanos)).UTC(), V: value.Elem().Interface()})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}
