package yoke

import (
	"context"
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yokecd/yoke/internal/x"
)

func TestSchematics(t *testing.T) {
	wasmPreamble := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}

	temp, err := os.CreateTemp("", "")
	require.NoError(t, err)

	_, _ = temp.Write(wasmPreamble)
	require.NoError(t, temp.Close())

	schematics, err := ListSchematics(context.Background(), ListSchematicsParams{WasmURL: temp.Name()})
	require.NoError(t, err)

	require.Equal(t, 0, len(schematics))

	require.NoError(t, SetSchematic(context.Background(), SetSchematicParams{
		WasmPath: temp.Name(),
		Name:     "test",
		Input:    strings.NewReader("This is a test!"),
	}))

	schematics, err = ListSchematics(context.Background(), ListSchematicsParams{WasmURL: temp.Name()})
	require.NoError(t, err)

	require.Equal(t, 1, len(schematics))
	require.Equal(t, "test", schematics[0])

	data, err := GetSchematic(context.Background(), GetSchematicParams{WasmURL: temp.Name(), Name: "test"})
	require.NoError(t, err)

	require.Equal(t, "This is a test!", string(data))
}

func TestSchematicsCMD(t *testing.T) {
	const modDir = "./test_output/mod"
	require.NoError(t, os.RemoveAll(modDir))
	require.NoError(t, os.MkdirAll("./test_output/mod", 0o755))

	require.NoError(t, x.X("go mod init temp", x.Dir(modDir)))
	mainDotGo, err := os.Create(path.Join(modDir, "main.go"))
	require.NoError(t, err)

	_, _ = io.WriteString(mainDotGo, `package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	docs := flag.Bool("mail", false, "print mail")
	flag.Parse()
	if *docs {
		fmt.Print("you've got mail!")
		os.Exit(0)
	}
	os.Exit(1)
}`)

	require.NoError(t, mainDotGo.Close())

	require.NoError(t, x.X("go build -o ./main.wasm ./main.go", x.Env("GOOS=wasip1", "GOARCH=wasm"), x.Dir(modDir)))

	require.NoError(t, SetSchematic(context.Background(), SetSchematicParams{
		WasmPath: "./test_output/mod/main.wasm",
		Name:     "mail",
		Input:    strings.NewReader("[-mail]"),
		CMD:      true,
	}))

	data, err := GetSchematic(context.Background(), GetSchematicParams{WasmURL: "./test_output/mod/main.wasm", Name: "mail"})
	require.NoError(t, err)

	require.Equal(t, "you've got mail!", string(data))
}
