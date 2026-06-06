package wasi_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/x"
)

func TestWasiExec(t *testing.T) {
	t.Run("readonly fs", func(t *testing.T) {
		require.NoError(t, x.X("go build -o ./test_output/fileops.wasm ./internal/testing/fileops", x.Env("GOOS=wasip1", "GOARCH=wasm")))

		wasm, err := os.ReadFile("./test_output/fileops.wasm")
		require.NoError(t, err)

		module, err := wasi.Compile(t.Context(), wasi.CompileParams{Wasm: wasm})
		require.NoError(t, err)

		data, err := wasi.Execute(t.Context(), wasi.ExecParams{
			Module: &module,
			FS: map[string]string{
				"./internal/testing/mount": "/app",
			},
		})
		require.NoError(t, err, string(data))
		require.Equal(t, "i am test", strings.TrimSpace(string(data)))

		_, err = wasi.Execute(t.Context(), wasi.ExecParams{
			Module: &module,
			FS:     map[string]string{"./internal/testing/mount": "/app"},
			Args:   []string{"-write"},
		})
		require.ErrorContains(t, err, "Bad file number")
	})
}
