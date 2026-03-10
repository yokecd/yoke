package yoke

import (
	"cmp"
	"fmt"
	"os"

	"github.com/yokecd/yoke/internal/xcrypto"
)

type SignParams struct {
	WasmFile string
	Out      string
	KeyPath  string
}

func Sign(params SignParams) error {
	wasm, err := os.ReadFile(params.WasmFile)
	if err != nil {
		return fmt.Errorf("failed to read wasm: %w", err)
	}

	pem, err := os.ReadFile(params.KeyPath)
	if err != nil {
		return fmt.Errorf("failed to read private key: %w", err)
	}

	key, err := xcrypto.ParsePrivateKeyFromPEM(pem)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	wasm, err = xcrypto.SignModule(key, wasm)
	if err != nil {
		return fmt.Errorf("failed to sign module: %w", err)
	}

	outfile := cmp.Or(params.Out, params.WasmFile)

	if err := os.WriteFile(outfile, wasm, 0o644); err != nil {
		return fmt.Errorf("failed to write signed module: %w", err)
	}

	return nil
}

type VerifyParams struct {
	WasmFile string
	KeyPath  string
}

func Verify(params VerifyParams) error {
	wasm, err := os.ReadFile(params.WasmFile)
	if err != nil {
		return fmt.Errorf("failed to read wasm: %w", err)
	}

	keys, err := xcrypto.LoadPublicKeysFromFS(params.KeyPath)
	if err != nil {
		return fmt.Errorf("failed to load public key(s): %w", err)
	}

	if err := xcrypto.VerifyModule(keys, wasm); err != nil {
		return fmt.Errorf("failed to verify module: %w", err)
	}

	return nil
}
