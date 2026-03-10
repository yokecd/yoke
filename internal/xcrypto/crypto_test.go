package xcrypto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicKeySetUmmarshalling(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		raw, err := json.Marshal([]string{
			`-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEApMDpIbcWU175oBrxH+TN
XZtA2dXRVTCINDlTXwRQyP/jiVwpqV33iZ6pdHFitDREs/l6N1fB1eYqKn2k05Qa
PmCBdWbLxkbdBcew0Gfbib3HE6bqODKv6E/L6TR7XxwxVKfc75GfnmR18qUBMzB5
g0/XrfUdudcprL1Kw4ESGNu+EUWe/3hQ4TaFPskQLlS6h4oYAoPGqjb9BmiS1f59
zv2ILpsWx7Gi1TxMsEXih8zuoMF9bSiPBo5dYHq4evEA+l8WL4IIYDEaQJs4GKMH
j1pvTz6H8QgcmvHj1I44lUFUXFNKmIaUtNBHqgKr9WGbluf0cbp7/a8zwvRhFgB4
JQIDAQAB
-----END PUBLIC KEY-----
`,
		})
		require.NoError(t, err)

		var keyset PublicKeySet
		require.NoError(t, json.Unmarshal(raw, &keyset))
		require.Len(t, keyset, 1)
	})

	t.Run("sad", func(t *testing.T) {
		raw, err := json.Marshal([]string{
			`-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEApMDpIbcWU175oBrxH+TN
XZtA2dXRVTCINDlTXwRQyP/jiVwpqV33iZ6pdHFitDREs/l6N1fB1eYqKn2k05Qa
PmCBdWbLxkbdBcew0Gfbib3HE6bqODKv6E/L6TR7XxwxVKfc75GfnmR18qUBMzB5
g0/XrfUdudcprL1Kw4ESGNu+EUWe/3hQ4TaFPskQLlS6h4oYAoPGqjb9BmiS1f59
zv2ILpsWx7Gi1TxMsEXih8zuoMF9bSiPBo5dYHq4evEA+l8WL4IIYDEaQJs4GKMH
j1pvTz6H8QgcmvHj1I44lUFUXFNKmIaUtNBHqgKr9WGbluf0cbp7/a8zwvRhFgB4
JQIDAQAB
-----END PUBLIC KEY-----
`,
			"potato",
		})
		require.NoError(t, err)

		var keyset PublicKeySet
		require.EqualError(
			t,
			json.Unmarshal(raw, &keyset),
			"failed to parse key at position 1: failed to decode PEM",
		)
	})
}
