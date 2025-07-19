package svr

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/internal/x"
	"github.com/yokecd/yoke/internal/xsync"
)

func TestPluginServer(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/echo.wasm ../testing/mods/echo", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	wasm, err := os.ReadFile("./test_output/echo.wasm")
	require.NoError(t, err)

	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(wasm)
	}))
	defer sourceServer.Close()

	var stdout bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&stdout, nil))

	mods := &xsync.Map[string, *Mod]{}

	modCount := func() (count int) {
		for range mods.All() {
			count++
		}
		return
	}

	svr := httptest.NewUnstartedServer(Handler(time.Second, mods, logger))
	defer svr.Close()

	listener, err := net.Listen("tcp", ":3666")
	require.NoError(t, err)

	// Match Exec's hardcoded default.
	svr.Listener = listener
	svr.Start()

	echo, err := Exec(context.Background(), ExecuteReq{
		Path:      sourceServer.URL,
		Release:   "foo",
		Namespace: "bar",
		Args:      []string{"a", "r", "g"},
		Env: map[string]string{
			"FOO": "BAR",
		},
		Input: "banana hamock",
	})
	require.NoError(t, err)

	require.JSONEq(
		t,
		`{
			"args": ["a","r","g"],
			"env": {
				"FOO":"BAR",
				"NAMESPACE":"bar",
				"YOKE_NAMESPACE":"bar",
				"YOKE_RELEASE":"foo",
				"YOKE_VERSION":"(devel)"
			 },
			"input":"banana hamock"
		}`,
		string(echo),
	)

	require.Equal(t, 1, modCount())

	_, err = Exec(context.Background(), ExecuteReq{
		Path:      sourceServer.URL,
		Release:   "baz",
		Namespace: "default",
	})
	require.NoError(t, err)

	require.Equal(t, 1, modCount())

	mods.Delete(sourceServer.URL)

	require.Equal(t, 0, modCount())

	_, err = Exec(context.Background(), ExecuteReq{
		Path:      sourceServer.URL,
		Release:   "baz",
		Namespace: "default",
	})
	require.NoError(t, err)

	type Log struct {
		CacheHit bool            `json:"cacheHit"`
		Elapsed  metav1.Duration `json:"elapsed"`
	}

	var logs []Log
	decoder := json.NewDecoder(&stdout)
	for {
		var log Log
		if err := decoder.Decode(&log); err == io.EOF {
			break
		}
		logs = append(logs, log)
	}

	require.Len(t, logs, 3)

	require.False(t, logs[0].CacheHit)
	require.True(t, logs[1].CacheHit)
	require.False(t, logs[2].CacheHit)

	require.True(t, logs[0].Elapsed.Duration > logs[1].Elapsed.Duration, "expected compile time to be greater than cache time")
}
