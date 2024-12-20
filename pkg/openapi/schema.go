package openapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
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

			fieldSchema := generateSchema(f.Type, false)

			if enum, ok := f.Tag.Lookup("Enum"); ok {
				elems := strings.Split(enum, ",")
				jsonElems := make([]apiext.JSON, len(elems))
				for i, elem := range elems {
					data, err := json.Marshal(elem)
					if err != nil {
						panic(fmt.Errorf("generate schema: field %q: %v", f.Name, err))
					}
					jsonElems[i].Raw = data
				}
				fieldSchema.Enum = jsonElems
			}

			if xvalidations, ok := f.Tag.Lookup("XValidations"); ok {
				var rules apiext.ValidationRules
				if err := json.Unmarshal([]byte(xvalidations), &rules); err != nil {
					panic(fmt.Errorf("generate schema: field %q: %v", f.Name, err))
				}
				fieldSchema.XValidations = rules
			}

			fieldValue := reflect.ValueOf(fieldSchema).Elem()

			for _, name := range []string{
				"Maximum",
				"Minimum",
				"MaxLength",
				"MinLength",
				"MaxItems",
				"MinItems",
				"UniqueItems",
				"Pattern",
				"ExclusiveMaximum",
				"ExclusiveMinimum",
				"MultipleOf",
				"Format",
			} {
				tag, ok := f.Tag.Lookup(name)
				if !ok {
					continue
				}

				fv := fieldValue.FieldByName(name)
				ft := fv.Type()
				if ft == nil {
					continue
				}

				for ft.Kind() == reflect.Pointer {
					if fv.IsNil() {
						fv.Set(reflect.New(ft.Elem()))
					}
					fv = fv.Elem()
					ft = ft.Elem()
				}

				// Limited type switch as these are the only types used for the above properties.
				switch ft.Kind() {
				case reflect.Int64:
					val, err := strconv.ParseInt(tag, 0, ft.Bits())
					if err != nil {
						panic(fmt.Errorf("generate schema: property %q: %v", name, err))
					}
					fv.SetInt(val)
				case reflect.Float64:
					val, err := strconv.ParseFloat(tag, ft.Bits())
					if err != nil {
						panic(fmt.Errorf("generate schema: property %q: %v", name, err))
					}
					fv.SetFloat(val)
				case reflect.Bool:
					val, err := strconv.ParseBool(tag)
					if err != nil {
						panic(fmt.Errorf("generate schema: property %q: %v", name, err))
					}
					fv.SetBool(val)
				case reflect.String:
					fv.SetString(tag)
				}

			}

			schema.Properties[key] = *fieldSchema
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
