// module serves to manipulate wasm module binaries and interact with them.
// Currently its only functionality is to set a custom section.
//
// The implementation was adapted from:
// https://github.com/hypermodeinc/modus/blob/b2a472c0cf869fab67868d8f5067a3bd44609e58/sdk/go/tools/modus-go-build/wasm/wasm.go#L33
package module

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/yokecd/yoke/internal"
)

const (
	PrefixSchematics    = "yoke.schematics:"
	PrefixSchematicsCMD = "yoke.schematics.cmd:"
)

func CutSchematicsPrefix(name string) (string, string, bool) {
	if value, ok := strings.CutPrefix(name, PrefixSchematics); ok {
		return PrefixSchematics, value, true
	}
	if value, ok := strings.CutPrefix(name, PrefixSchematicsCMD); ok {
		return PrefixSchematicsCMD, value, true
	}
	return "", name, false
}

func StripSchematicsPrefix(name string) string {
	if value, ok := strings.CutPrefix(name, PrefixSchematics); ok {
		return value
	}
	if value, ok := strings.CutPrefix(name, PrefixSchematicsCMD); ok {
		return value
	}
	return name
}

func GetCustomSections(wasm []byte) map[string][]byte {
	sections := map[string][]byte{}

	offset := 8 // Skip Preamble

	for offset < len(wasm) {
		start := offset
		id := wasm[start]
		offset++

		size, n := binary.Uvarint(wasm[offset:])
		offset += n

		if id == 0 {
			nameSize, n := binary.Uvarint(wasm[offset:])
			name := string(wasm[offset+n : offset+n+int(nameSize)])
			if slices.ContainsFunc([]string{PrefixSchematics, PrefixSchematicsCMD}, func(prefix string) bool {
				return strings.HasPrefix(name, prefix)
			}) {
				data := wasm[offset+n+int(nameSize) : offset+int(size)]
				sections[name] = data
			}
		}

		offset += int(size)
	}

	return sections
}

func withCustomSection(wasm []byte, prefix, name string, data []byte) []byte {
	var buffer bytes.Buffer

	// Preamble
	_, _ = buffer.Write(wasm[:8])
	offset := 8

	// Sections
	for offset < len(wasm) {
		secStart := offset

		sectionID := wasm[offset]
		offset++

		size, n := binary.Uvarint(wasm[offset:])
		offset += n

		// Skip existing custom section with the same names as the new ones
		if sectionID == 0 {
			nameLen, n := binary.Uvarint(wasm[offset:])
			nameBytes := wasm[offset+n : offset+n+int(nameLen)]
			if slices.ContainsFunc([]string{PrefixSchematicsCMD, PrefixSchematics}, func(prefix string) bool {
				key, ok := strings.CutPrefix(string(nameBytes), prefix)
				return ok && key == name
			}) {
				offset += int(size)
				continue
			}
		}

		offset += int(size)
		_, _ = buffer.Write(wasm[secStart:offset])
	}

	name = prefix + name
	nameVarLength := makeUvarint(len(name))
	payloadSize := len(nameVarLength) + len(name) + len(data)
	size := makeUvarint(payloadSize)

	_ = buffer.WriteByte(0)
	_, _ = buffer.Write(size)
	_, _ = buffer.Write(nameVarLength)
	_, _ = buffer.WriteString(name)
	_, _ = buffer.Write(data)

	return buffer.Bytes()
}

func WithCustomSectionCMD(wasm []byte, name string, cmd []string) []byte {
	return withCustomSection(wasm, PrefixSchematicsCMD, name, internal.Must2(json.Marshal(cmd)))
}

func WithCustomSectionData(wasm []byte, name string, data []byte) []byte {
	return withCustomSection(wasm, PrefixSchematics, name, data)
}

func makeUvarint(x int) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(buf, uint64(x))
	return buf[:n]
}

func ValidatePreamble(wasm []byte) error {
	magic := []byte{0x00, 0x61, 0x73, 0x6D} // "\0asm"
	if len(wasm) < 8 || !bytes.Equal(wasm[:4], magic) {
		return fmt.Errorf("invalid wasm file")
	}
	if binary.LittleEndian.Uint32(wasm[4:8]) != 1 {
		return fmt.Errorf("unsupported wasm version")
	}
	return nil
}
