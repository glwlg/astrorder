package hermes

import "testing"

func TestSubagentAndTodoEventsForwardWithoutChangingParentStatus(t *testing.T){
 var types []string
 a:=New(Config{AgentID:"a",Emit:func(sid,kind string,p map[string]any,status string)error{
  if sid!="s" || kind!="hermes.notification" || status!="idle"{t.Errorf("wrong scope/status %s %s %s",sid,kind,status)}
  frame,_:=p["frame"].(map[string]any);types=append(types,frame["type"].(string));return nil
 }})
 a.sessions["s"]=&SessionState{SessionID:"s",Status:"idle"}
 a.handleToSession["handle"]="s"
 for _,kind:=range []string{"subagent.start","subagent.progress","subagent.complete","todo.updated"}{a.handleGatewayEvent(map[string]any{"type":kind,"session_id":"handle","payload":map[string]any{"subagent_id":"child"}})}
 if len(types)!=4{t.Fatalf("native runtime events silently dropped: %v",types)}
 a.handleGatewayEvent(map[string]any{"type":"subagent.start","session_id":"foreign","payload":map[string]any{}})
 if len(types)!=4{t.Fatal("foreign event forwarded")}
}
