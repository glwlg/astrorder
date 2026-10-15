package codex

import (
 "context"
 "encoding/json"
 "testing"
 "time"
 core "astrorder.dev/session-daemon/internal/runtime"
 "astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestGoalPauseResumePreservesObjectiveAndBudget(t *testing.T) {
 fake := newFakeSSHSession()
 go func() {
  decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
  goal := map[string]any{"threadId":"s", "objective":"keep this", "status":"active", "tokenBudget":500, "tokensUsed":200}
  for {
   var req map[string]any
   if decoder.Decode(&req) != nil { return }
   result := map[string]any{}
   switch req["method"] {
   case "initialized": continue
   case "initialize":
   case "thread/resume": result["thread"] = map[string]any{"id":"s"}
   case "thread/goal/get": result["goal"] = goal
   case "thread/goal/set":
    params := req["params"].(map[string]any)
    if params["objective"] != "keep this" || params["tokenBudget"] != float64(500) || params["origin"] != "user" { t.Error("lost state or explicit user provenance", params) }
    goal["status"] = params["status"]
    result["goal"] = goal
   default: t.Error("unexpected native call", req["method"])
   }
   if encoder.Encode(map[string]any{"id":req["id"], "result":result}) != nil { return }
  }
 }()
 a := NewSSHAdapter(Config{Executable:"/fixture/codex", Workspace:"/work", Allowed:[]string{"/work"}, AgentID:"a"}, nil, func(context.Context, ssh.CommandSpec)(SSHSession,error){return fake,nil})
 defer a.Close()
 ctx,cancel := context.WithTimeout(context.Background(),4*time.Second); defer cancel()
 if _,err:=a.Spawn(ctx,core.Request{SessionID:"s"}); err!=nil {t.Fatal(err)}
 for _,state:=range []string{"paused","active"} {
  result,err:=a.Command(ctx,core.Request{SessionID:"s",Action:"session.goal.set",Fields:map[string]any{"status":state}})
  if err!=nil {t.Fatal(state,err)}
  if goal,_:=result.Payload["goal"].(map[string]any); goal["status"]!=state || goal["objective"]!="keep this" || goal["tokensUsed"]!=json.Number("200") {t.Fatalf("readback: %+v",goal)}
 }
}
