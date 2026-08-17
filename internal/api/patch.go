package api

import (
	"encoding/json"
	"io"
	"net/http"
)

// Patching, rather than replacing, is how a stored record survives an incomplete request.
//
// Every config endpoint here used to decode the request into an empty struct and write that
// over what was stored, which made the browser's in-memory form the system of record: any
// field the client failed to send was silently reset to its zero value. That is not
// theoretical — it turned a dry-run task into a real one and moved files. Other fields had
// been lost the same way earlier and were rescued one at a time, which is why the options
// handler carried hand-written lines copying values back.
//
// A patch decodes onto the CURRENT record, so an absent field means "leave it alone" and only
// what the request actually carried can change.

// readBody reads a request body with a sane ceiling, so a patch cannot be used to make the
// process read an unbounded amount.
func readBody(req *http.Request) ([]byte, error) {
	return io.ReadAll(io.LimitReader(req.Body, 1<<20))
}

// patchOnto decodes body over a copy of stored and returns the result. Nested objects merge
// field by field, which is what a partial form submission means; arrays are replaced whole,
// since a list has no meaningful per-element merge.
func patchOnto[T any](stored T, body []byte) (T, error) {
	out := stored
	if err := json.Unmarshal(body, &out); err != nil {
		return stored, err
	}
	return out, nil
}

// sentKeys reports which top-level fields a request actually mentioned. Needed when "absent"
// and "explicitly empty" have to be told apart — a nested object that was sent at all should
// replace rather than merge, so half of it cannot inherit the other half of the old one.
func sentKeys(body []byte) map[string]bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return nil
	}
	out := make(map[string]bool, len(raw))
	for k := range raw {
		out[k] = true
	}
	return out
}
