package openapi_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/openapi"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/utils/ptr"
)

func TestGenerateSchema(t *testing.T) {
	type Embedded struct {
		Embedded bool `json:"embed"`
	}

	type S struct {
		Embedded
		Name   string            `json:"name" MinLength:"3"`
		Age    int               `json:"age" Minimum:"18"`
		Labels map[string]string `json:"labels,omitempty"`
		Active bool              `json:"active"`
		Choice string            `json:"choice" Enum:"yes,no,toaster"`
		Rule   string            `json:"rule" XValidations:"[{\"rule\": \"has(self)\", \"message\":\"something\"}]"`
	}

	require.EqualValues(
		t,
		&apiext.JSONSchemaProps{
			Type: "object",
			Properties: apiext.JSONSchemaDefinitions{
				"name": {
					Type:      "string",
					MinLength: ptr.To[int64](3),
				},
				"age": {
					Type:    "integer",
					Minimum: ptr.To[float64](18),
				},
				"active": {
					Type: "boolean",
				},
				"labels": {
					Type: "object",
					AdditionalProperties: &apiext.JSONSchemaPropsOrBool{
						Schema: &apiext.JSONSchemaProps{Type: "string"},
					},
				},
				"choice": {
					Type: "string",
					Enum: []apiext.JSON{
						{Raw: []byte(`"yes"`)},
						{Raw: []byte(`"no"`)},
						{Raw: []byte(`"toaster"`)},
					},
				},
				"rule": {
					Type: "string",
					XValidations: apiext.ValidationRules{
						{
							Rule:    "has(self)",
							Message: "something",
						},
					},
				},
				"embed": {
					Type: "boolean",
				},
			},
			Required: []string{"name", "age", "active", "choice", "rule"},
		},
		openapi.SchemaFrom(reflect.TypeOf(S{})),
	)
}

func TestAirwaySchema(t *testing.T) {
	schema := openapi.SchemaFrom(reflect.TypeFor[v1alpha1.Airway]())

	data, err := json.MarshalIndent(schema, "", "  ")
	require.NoError(t, err)

	require.JSONEq(t, string(data), `{
  "type": "object",
  "required": [
    "spec"
  ],
  "properties": {
    "spec": {
      "type": "object",
      "required": [
        "wasmUrls",
        "template"
      ],
      "properties": {
        "createCrds": {
          "type": "boolean"
        },
        "fixDriftInterval": {
          "type": "string"
        },
        "objectPath": {
          "type": "array",
          "items": {
            "type": "string"
          }
        },
        "template": {
          "type": "object",
          "required": [
            "group",
            "names",
            "scope",
            "versions"
          ],
          "properties": {
            "conversion": {
              "type": "object",
              "required": [
                "strategy"
              ],
              "properties": {
                "strategy": {
                  "type": "string"
                },
                "webhook": {
                  "type": "object",
                  "required": [
                    "conversionReviewVersions"
                  ],
                  "properties": {
                    "clientConfig": {
                      "type": "object",
                      "properties": {
                        "caBundle": {
                          "type": "array",
                          "items": {
                            "type": "integer"
                          }
                        },
                        "service": {
                          "type": "object",
                          "required": [
                            "namespace",
                            "name"
                          ],
                          "properties": {
                            "name": {
                              "type": "string"
                            },
                            "namespace": {
                              "type": "string"
                            },
                            "path": {
                              "type": "string"
                            },
                            "port": {
                              "type": "integer"
                            }
                          }
                        },
                        "url": {
                          "type": "string"
                        }
                      }
                    },
                    "conversionReviewVersions": {
                      "type": "array",
                      "items": {
                        "type": "string"
                      }
                    }
                  }
                }
              }
            },
            "group": {
              "type": "string"
            },
            "names": {
              "type": "object",
              "required": [
                "plural",
                "kind"
              ],
              "properties": {
                "categories": {
                  "type": "array",
                  "items": {
                    "type": "string"
                  }
                },
                "kind": {
                  "type": "string"
                },
                "listKind": {
                  "type": "string"
                },
                "plural": {
                  "type": "string"
                },
                "shortNames": {
                  "type": "array",
                  "items": {
                    "type": "string"
                  }
                },
                "singular": {
                  "type": "string"
                }
              }
            },
            "preserveUnknownFields": {
              "type": "boolean"
            },
            "scope": {
              "type": "string"
            },
            "versions": {
              "type": "array",
              "items": {
                "type": "object",
                "required": [
                  "name",
                  "served",
                  "storage"
                ],
                "properties": {
                  "additionalPrinterColumns": {
                    "type": "array",
                    "items": {
                      "type": "object",
                      "required": [
                        "name",
                        "type",
                        "jsonPath"
                      ],
                      "properties": {
                        "description": {
                          "type": "string"
                        },
                        "format": {
                          "type": "string"
                        },
                        "jsonPath": {
                          "type": "string"
                        },
                        "name": {
                          "type": "string"
                        },
                        "priority": {
                          "type": "integer"
                        },
                        "type": {
                          "type": "string"
                        }
                      }
                    }
                  },
                  "deprecated": {
                    "type": "boolean"
                  },
                  "deprecationWarning": {
                    "type": "string"
                  },
                  "name": {
                    "type": "string"
                  },
                  "schema": {
                    "type": "object",
                    "properties": {
                      "openAPIV3Schema": {
                        "type": "object",
                        "properties": {
                          "$ref": {
                            "type": "string"
                          },
                          "$schema": {
                            "type": "string"
                          },
                          "additionalItems": {
                            "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaPropsOrBool",
                            "type": "object",
                            "x-kubernetes-preserve-unknown-fields": true
                          },
                          "additionalProperties": {
                            "type": "object",
                            "required": [
                              "Allows"
                            ],
                            "properties": {
                              "Allows": {
                                "type": "boolean"
                              },
                              "Schema": {
                                "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                                "type": "object",
                                "x-kubernetes-preserve-unknown-fields": true
                              }
                            }
                          },
                          "allOf": {
                            "type": "array",
                            "items": {
                              "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                              "type": "object",
                              "x-kubernetes-preserve-unknown-fields": true
                            }
                          },
                          "anyOf": {
                            "type": "array",
                            "items": {
                              "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                              "type": "object",
                              "x-kubernetes-preserve-unknown-fields": true
                            }
                          },
                          "default": {
                            "type": "object",
                            "required": [
                              "-"
                            ],
                            "properties": {
                              "-": {
                                "type": "array",
                                "items": {
                                  "type": "integer"
                                }
                              }
                            }
                          },
                          "definitions": {
                            "type": "object",
                            "additionalProperties": {
                              "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                              "type": "object",
                              "x-kubernetes-preserve-unknown-fields": true
                            }
                          },
                          "dependencies": {
                            "type": "object",
                            "additionalProperties": {
                              "type": "object",
                              "required": [
                                "Property"
                              ],
                              "properties": {
                                "Property": {
                                  "type": "array",
                                  "items": {
                                    "type": "string"
                                  }
                                },
                                "Schema": {
                                  "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                                  "type": "object",
                                  "x-kubernetes-preserve-unknown-fields": true
                                }
                              }
                            }
                          },
                          "description": {
                            "type": "string"
                          },
                          "enum": {
                            "type": "array",
                            "items": {
                              "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSON",
                              "type": "object",
                              "x-kubernetes-preserve-unknown-fields": true
                            }
                          },
                          "example": {
                            "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSON",
                            "type": "object",
                            "x-kubernetes-preserve-unknown-fields": true
                          },
                          "exclusiveMaximum": {
                            "type": "boolean"
                          },
                          "exclusiveMinimum": {
                            "type": "boolean"
                          },
                          "externalDocs": {
                            "type": "object",
                            "properties": {
                              "description": {
                                "type": "string"
                              },
                              "url": {
                                "type": "string"
                              }
                            }
                          },
                          "format": {
                            "type": "string"
                          },
                          "id": {
                            "type": "string"
                          },
                          "items": {
                            "type": "object",
                            "required": [
                              "JSONSchemas"
                            ],
                            "properties": {
                              "JSONSchemas": {
                                "type": "array",
                                "items": {
                                  "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                                  "type": "object",
                                  "x-kubernetes-preserve-unknown-fields": true
                                }
                              },
                              "Schema": {
                                "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                                "type": "object",
                                "x-kubernetes-preserve-unknown-fields": true
                              }
                            }
                          },
                          "maxItems": {
                            "type": "integer"
                          },
                          "maxLength": {
                            "type": "integer"
                          },
                          "maxProperties": {
                            "type": "integer"
                          },
                          "maximum": {
                            "type": "number"
                          },
                          "minItems": {
                            "type": "integer"
                          },
                          "minLength": {
                            "type": "integer"
                          },
                          "minProperties": {
                            "type": "integer"
                          },
                          "minimum": {
                            "type": "number"
                          },
                          "multipleOf": {
                            "type": "number"
                          },
                          "not": {
                            "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                            "type": "object",
                            "x-kubernetes-preserve-unknown-fields": true
                          },
                          "nullable": {
                            "type": "boolean"
                          },
                          "oneOf": {
                            "type": "array",
                            "items": {
                              "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                              "type": "object",
                              "x-kubernetes-preserve-unknown-fields": true
                            }
                          },
                          "pattern": {
                            "type": "string"
                          },
                          "patternProperties": {
                            "type": "object",
                            "additionalProperties": {
                              "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                              "type": "object",
                              "x-kubernetes-preserve-unknown-fields": true
                            }
                          },
                          "properties": {
                            "type": "object",
                            "additionalProperties": {
                              "description": "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1:JSONSchemaProps",
                              "type": "object",
                              "x-kubernetes-preserve-unknown-fields": true
                            }
                          },
                          "required": {
                            "type": "array",
                            "items": {
                              "type": "string"
                            }
                          },
                          "title": {
                            "type": "string"
                          },
                          "type": {
                            "type": "string"
                          },
                          "uniqueItems": {
                            "type": "boolean"
                          },
                          "x-kubernetes-embedded-resource": {
                            "type": "boolean"
                          },
                          "x-kubernetes-int-or-string": {
                            "type": "boolean"
                          },
                          "x-kubernetes-list-map-keys": {
                            "type": "array",
                            "items": {
                              "type": "string"
                            }
                          },
                          "x-kubernetes-list-type": {
                            "type": "string"
                          },
                          "x-kubernetes-map-type": {
                            "type": "string"
                          },
                          "x-kubernetes-preserve-unknown-fields": {
                            "type": "boolean"
                          },
                          "x-kubernetes-validations": {
                            "type": "array",
                            "items": {
                              "type": "object",
                              "required": [
                                "rule"
                              ],
                              "properties": {
                                "fieldPath": {
                                  "type": "string"
                                },
                                "message": {
                                  "type": "string"
                                },
                                "messageExpression": {
                                  "type": "string"
                                },
                                "optionalOldSelf": {
                                  "type": "boolean"
                                },
                                "reason": {
                                  "type": "string"
                                },
                                "rule": {
                                  "type": "string"
                                }
                              }
                            }
                          }
                        }
                      }
                    }
                  },
                  "selectableFields": {
                    "type": "array",
                    "items": {
                      "type": "object",
                      "required": [
                        "jsonPath"
                      ],
                      "properties": {
                        "jsonPath": {
                          "type": "string"
                        }
                      }
                    }
                  },
                  "served": {
                    "type": "boolean"
                  },
                  "storage": {
                    "type": "boolean"
                  },
                  "subresources": {
                    "type": "object",
                    "properties": {
                      "scale": {
                        "type": "object",
                        "required": [
                          "specReplicasPath",
                          "statusReplicasPath"
                        ],
                        "properties": {
                          "labelSelectorPath": {
                            "type": "string"
                          },
                          "specReplicasPath": {
                            "type": "string"
                          },
                          "statusReplicasPath": {
                            "type": "string"
                          }
                        }
                      },
                      "status": {
                        "type": "object"
                      }
                    }
                  }
                }
              }
            }
          }
        },
        "wasmUrls": {
          "type": "object",
          "required": [
            "flight"
          ],
          "properties": {
            "converter": {
              "type": "string"
            },
            "flight": {
              "type": "string"
            }
          }
        }
      }
    },
    "status": {
      "type": "object",
      "properties": {
        "msg": {
          "type": "string"
        },
        "status": {
          "type": "string"
        }
      }
    }
  }
}`)
}
