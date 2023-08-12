package utils

import "reflect"

func ToMap(s any) (m map[string]any) {
	v := reflect.Indirect(reflect.ValueOf(s))
	if v.Kind() == reflect.Map {
		m = s.(map[string]any)
		return
	}
	if v.Kind() != reflect.Struct {
		return
	}

	m = make(map[string]any)
	vType := v.Type()
	for i := 0; i < v.NumField(); i++ {
		m[vType.Field(i).Name] = v.Field(i).Interface()
	}
	return
}
