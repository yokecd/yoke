package validation

import (
	"github.com/davidmdm/x/xerr"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	validation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"

	"github.com/yokecd/yoke/pkg/apis"
)

// Validate verifies that the value matches the schema.
// If the schema was generated using the yoke openapi package, unknown fields are not errors.
// To validate no unknown fields are present use ValidateStrict.
func Validate(props *apiextv1.JSONSchemaProps, value any) error {
	var internal apiext.JSONSchemaProps
	if err := apiextv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(props, &internal, nil); err != nil {
		return err
	}
	validator, _, err := validation.NewSchemaValidator(&internal)
	if err != nil {
		return err
	}
	var (
		fieldErrors = validation.ValidateCustomResource(nil, value, validator)
		errs        = make([]error, len(fieldErrors))
	)
	for i, e := range fieldErrors {
		errs[i] = e
	}
	return xerr.Join(errs...)
}

// ValidateStrict deep copies the schema and modifies the copy such that additionalProperties are not allowed for objects that don't define any.
// By default objects are open. This closes them for extension.
// This is the same as validating against the validation.Strict(schema).
func ValidateStrict(props *apiextv1.JSONSchemaProps, value any) error {
	return Validate(Strict(props), value)
}

// Strict returns a deep copy of the schema but modified so that all objects are closed for extension unless otherwise specified.
func Strict(schema *apiextv1.JSONSchemaProps) *apiextv1.JSONSchemaProps {
	return strict(apis.DeepCopy(schema))
}

func strict(schema *apiextv1.JSONSchemaProps) *apiextv1.JSONSchemaProps {
	if schema == nil {
		return nil
	}
	switch schema.Type {
	case "object":
		preserveFields := schema.XPreserveUnknownFields != nil && *schema.XPreserveUnknownFields
		additionalProps := schema.AdditionalProperties != nil && (schema.AdditionalProperties.Schema != nil || schema.AdditionalProperties.Allows)
		if !preserveFields && !additionalProps {
			schema.AdditionalProperties = &apiextv1.JSONSchemaPropsOrBool{Allows: false}
		}
		for name, field := range schema.Properties {
			schema.Properties[name] = *strict(&field)
		}
	case "array":
		strict(schema.Items.Schema)
		for i := range schema.Items.JSONSchemas {
			strict(&schema.Items.JSONSchemas[i])
		}
	}
	return schema
}
