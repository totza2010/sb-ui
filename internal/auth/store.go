package auth

import "sb-ui/internal/store"

// Persistence lives behind the readState/writeState seams so the rules above can be tested
// without a host to write to. On a real system this is the same /opt/saltbox-ui state
// directory everything else uses, so the password survives a reinstall of the binary.

func defaultRead() state {
	var s state
	store.ReadJSON(storeRel, &s)
	return s
}

func defaultWrite(s state) { store.WriteJSON(storeRel, s) }

// UseMemoryStore swaps persistence for an in-memory copy and resets the loaded state.
// Tests call it; nothing in production does.
func UseMemoryStore() {
	mu.Lock()
	defer mu.Unlock()
	var mem state
	readState = func() state { return mem }
	writeState = func(s state) { mem = s }
	st, loaded = state{}, false
}
