package session

import (
	"maps"
	"path"
	"sync"
	"sync/atomic"

	_ "embed"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/tarpack"
)

// The text of the verb docs: each verb's description, the description of
// each parameter and result field, and the examples. It is kept out of the Go
// registry, in a .verbs.json file beside the Go file that registers the verbs,
// and packed into one gzipped archive. Only list-verbs, tuios list-verbs and
// the tuios mcp tool list read it, so the daemon and every other command pay
// for the compressed bytes only. TestVerbDocsMatchTheRegistry holds the files
// to the registry: every verb has a doc, every doc names a verb, and the
// parameter and result names are the same, in the same order.

//go:generate go run ../tarpack/gen.go verbdocs.tar.gz *.verbs.json

//go:embed verbdocs.tar.gz
var verbDocsArchive []byte

// verbDocText is one verb's entry in a .verbs.json file.
type verbDocText struct {
	Description string          `json:"description"`
	Params      []verbFieldText `json:"params,omitempty"`
	Returns     []verbFieldText `json:"returns,omitempty"`
	Examples    []string        `json:"examples"`
}

// verbFieldText is the description of one parameter or result field.
type verbFieldText struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// verbDocTexts returns the text of every verb's doc, by verb name. The
// archive is unpacked and decoded on the first call and kept.
var verbDocTexts = sync.OnceValue(func() map[string]verbDocText {
	verbDocsDecoded.Store(true)
	docs, err := decodeVerbDocs(verbDocsArchive)
	if err != nil {
		// The archive is built and checked with the binary, so this is a
		// broken build. The verbs still dispatch, with empty docs.
		LogBasic("verb docs: %v", err)
	}
	return docs
})

// verbDocsDecoded is set once the docs are decoded, for the test that holds
// the daemon to decoding them only when asked.
var verbDocsDecoded atomic.Bool

// decodeVerbDocs reads every .verbs.json file in the archive into one map.
func decodeVerbDocs(archive []byte) (map[string]verbDocText, error) {
	files, err := tarpack.Unpack(archive)
	if err != nil {
		return nil, err
	}
	docs := map[string]verbDocText{}
	for name, data := range files {
		if path.Ext(name) != ".json" {
			continue
		}
		var part map[string]verbDocText
		if err := jsonx.Unmarshal(data, &part); err != nil {
			return docs, err
		}
		maps.Copy(docs, part)
	}
	return docs, nil
}

// withDescriptions returns a copy of fields with each description set from
// text, which lists the same names in the same order. A field whose name does
// not match keeps an empty description. A nil fields stays nil.
func withDescriptions(fields []verbParam, text []verbFieldText) []verbParam {
	if fields == nil {
		return nil
	}
	out := make([]verbParam, len(fields))
	copy(out, fields)
	for i := range out {
		if i < len(text) && text[i].Name == out[i].Name {
			out[i].Description = text[i].Description
		}
	}
	return out
}
