// Code generated by "libovsdb.modelgen"
// DO NOT EDIT.

package ovsmodel

import (
	"encoding/json"

	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
)

// FullDatabaseModel returns the DatabaseModel object to be used in libovsdb
func FullDatabaseModel() (model.ClientDBModel, error) {
	return model.NewClientDBModel("OVN_IC_Northbound", map[string]model.Model{
		"Connection":     &Connection{},
		"IC_NB_Global":   &ICNBGlobal{},
		"SSL":            &SSL{},
		"Transit_Switch": &TransitSwitch{},
	})
}

var schema = `{
  "name": "OVN_IC_Northbound",
  "version": "1.0.0",
  "tables": {
    "Connection": {
      "columns": {
        "external_ids": {
          "type": {
            "key": {
              "type": "string"
            },
            "value": {
              "type": "string"
            },
            "min": 0,
            "max": "unlimited"
          }
        },
        "inactivity_probe": {
          "type": {
            "key": {
              "type": "integer"
            },
            "min": 0,
            "max": 1
          }
        },
        "is_connected": {
          "type": "boolean",
          "ephemeral": true
        },
        "max_backoff": {
          "type": {
            "key": {
              "type": "integer",
              "minInteger": 1000
            },
            "min": 0,
            "max": 1
          }
        },
        "other_config": {
          "type": {
            "key": {
              "type": "string"
            },
            "value": {
              "type": "string"
            },
            "min": 0,
            "max": "unlimited"
          }
        },
        "status": {
          "type": {
            "key": {
              "type": "string"
            },
            "value": {
              "type": "string"
            },
            "min": 0,
            "max": "unlimited"
          },
          "ephemeral": true
        },
        "target": {
          "type": "string"
        }
      },
      "indexes": [
        [
          "target"
        ]
      ]
    },
    "IC_NB_Global": {
      "columns": {
        "connections": {
          "type": {
            "key": {
              "type": "uuid",
              "refTable": "Connection"
            },
            "min": 0,
            "max": "unlimited"
          }
        },
        "external_ids": {
          "type": {
            "key": {
              "type": "string"
            },
            "value": {
              "type": "string"
            },
            "min": 0,
            "max": "unlimited"
          }
        },
        "options": {
          "type": {
            "key": {
              "type": "string"
            },
            "value": {
              "type": "string"
            },
            "min": 0,
            "max": "unlimited"
          }
        },
        "ssl": {
          "type": {
            "key": {
              "type": "uuid",
              "refTable": "SSL"
            },
            "min": 0,
            "max": 1
          }
        }
      },
      "isRoot": true
    },
    "SSL": {
      "columns": {
        "bootstrap_ca_cert": {
          "type": "boolean"
        },
        "ca_cert": {
          "type": "string"
        },
        "certificate": {
          "type": "string"
        },
        "external_ids": {
          "type": {
            "key": {
              "type": "string"
            },
            "value": {
              "type": "string"
            },
            "min": 0,
            "max": "unlimited"
          }
        },
        "private_key": {
          "type": "string"
        },
        "ssl_ciphers": {
          "type": "string"
        },
        "ssl_protocols": {
          "type": "string"
        }
      }
    },
    "Transit_Switch": {
      "columns": {
        "external_ids": {
          "type": {
            "key": {
              "type": "string"
            },
            "value": {
              "type": "string"
            },
            "min": 0,
            "max": "unlimited"
          }
        },
        "name": {
          "type": "string"
        },
        "other_config": {
          "type": {
            "key": {
              "type": "string"
            },
            "value": {
              "type": "string"
            },
            "min": 0,
            "max": "unlimited"
          }
        }
      },
      "indexes": [
        [
          "name"
        ]
      ],
      "isRoot": true
    }
  }
}`

func Schema() ovsdb.DatabaseSchema {
	var s ovsdb.DatabaseSchema
	err := json.Unmarshal([]byte(schema), &s)
	if err != nil {
		panic(err)
	}
	return s
}
