package openapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

var cache = map[reflect.Type]*apiext.JSONSchemaProps{}

func SchemaFrom(typ reflect.Type) *apiext.JSONSchemaProps {
	return generateSchema(typ, true)
}

func generateSchema(typ reflect.Type, top bool) *apiext.JSONSchemaProps {
	type OpenAPISchemer interface {
		OpenAPISchema() *apiext.JSONSchemaProps
	}

	if value, ok := reflect.New(typ).Elem().Interface().(OpenAPISchemer); ok {
		return value.OpenAPISchema()
	}

	switch typ.Kind() {
	case reflect.String:
		return &apiext.JSONSchemaProps{Type: "string"}

	case reflect.Int, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &apiext.JSONSchemaProps{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return &apiext.JSONSchemaProps{Type: "number"}
	case reflect.Bool:
		return &apiext.JSONSchemaProps{Type: "boolean"}
	case reflect.Interface:
		return &apiext.JSONSchemaProps{}
	case reflect.Map:
		return &apiext.JSONSchemaProps{
			Type: "object",
			AdditionalProperties: &apiext.JSONSchemaPropsOrBool{
				Schema: func() *apiext.JSONSchemaProps {
					if _, ok := cache[typ.Elem()]; ok {
						return &apiext.JSONSchemaProps{
							Type:                   "object",
							XPreserveUnknownFields: ptr(true),
							Description:            fmt.Sprintf("%s:%s", typ.Elem().PkgPath(), typ.Elem().Name()),
						}
					}
					return generateSchema(typ.Elem(), false)
				}(),
			},
		}
	case reflect.Slice:
		return &apiext.JSONSchemaProps{
			Type: "array",
			Items: &apiext.JSONSchemaPropsOrArray{
				Schema: func() *apiext.JSONSchemaProps {
					if _, ok := cache[typ.Elem()]; ok {
						return &apiext.JSONSchemaProps{
							Type:                   "object",
							XPreserveUnknownFields: ptr(true),
							Description:            fmt.Sprintf("%s:%s", typ.Elem().PkgPath(), typ.Elem().Name()),
						}
					}
					return generateSchema(typ.Elem(), false)
				}(),
			},
		}
	case reflect.Struct:
		if _, ok := cache[typ]; !ok {
			cache[typ] = nil
		}
		schema := &apiext.JSONSchemaProps{
			Type:       "object",
			Properties: apiext.JSONSchemaDefinitions{},
		}

		for i := range typ.NumField() {
			f := typ.Field(i)

			jTag := f.Tag.Get("json")

			key, _, _ := strings.Cut(jTag, ",")
			if key == "" {
				key = f.Name
			}

			if top && slices.Contains([]string{"ObjectMeta", "TypeMeta"}, f.Name) {
				continue
			}

			if !strings.HasSuffix(jTag, ",omitempty") && f.Type.Kind() != reflect.Pointer {
				schema.Required = append(schema.Required, key)
			}

			schema.Properties[key] = *generateSchema(f.Type, false)
		}

		cache[typ] = schema

		return schema

	case reflect.Pointer:
		if _, ok := cache[typ.Elem()]; ok {
			return &apiext.JSONSchemaProps{
				Type:                   "object",
				XPreserveUnknownFields: ptr(true),
				Description:            fmt.Sprintf("%s:%s", typ.Elem().PkgPath(), typ.Elem().Name()),
			}
		}
		return generateSchema(typ.Elem(), false)
	}

	panic("unreachable: " + typ.Kind().String())
}

func ptr[T any](value T) *T { return &value }

type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}

	value, err := time.ParseDuration(str)
	if err != nil {
		return err
	}
	*d = Duration(value)

	return nil
}

func (Duration) OpenAPISchema() *apiext.JSONSchemaProps {
	return &apiext.JSONSchemaProps{Type: "string"}
}

func (d Duration) Duration() time.Duration { return time.Duration(d) }
