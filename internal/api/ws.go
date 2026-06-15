package api

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"

	"sb-ui/internal/jobs"
)

// jobWS streams a job's log: replays history, then live messages until the job
// finishes or the client disconnects.
func jobWS(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	c, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()

	snapshot, ch, cancel, ok := jobs.Subscribe(id)
	if !ok {
		_ = c.Close(websocket.StatusNormalClosure, "job not found")
		return
	}
	defer cancel()

	ctx := req.Context()
	for _, line := range snapshot {
		if err := wsjson.Write(ctx, c, jobs.Msg{Type: "log", Line: line}); err != nil {
			return
		}
	}
	for msg := range ch {
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		err := wsjson.Write(wctx, c, msg)
		wcancel()
		if err != nil {
			return
		}
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}
