package sessions

import (
 "context"
 "testing"
 "astrorder.dev/app-server/internal/application"
 "astrorder.dev/app-server/internal/store"
 pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestHermesApprovalRefreshUsesMetadataOnlyNativeQuery(t *testing.T) {
 called:=false
 server:=&mockDaemonServer{queryCb:func(r *pb.NativeQueryRequest)(*pb.NativeQueryResponse,error){
  q:=r.GetHermesSessionHistory()
  if q==nil || q.SessionId!="native" || string(q.Options.Utf8Json)!=`{"omit_messages":true}` {t.Fatalf("unsafe snapshot request: %v",r)}
  called=true
  return &pb.NativeQueryResponse{Result:&pb.NativeJson{Utf8Json:[]byte(`{"result":{"session_id":"native","revision":2,"execution_generation":3,"prompts":[]}}`)}},nil
 }}
 h,st,_,cleanup:=setupTestServer(t,server);defer cleanup();h.publisher=application.NewEventHub(st)
 ctx:=context.Background()
 if _,err:=st.UpsertAgent(ctx,map[string]any{"id":"a","kind":"hermes","status":"ready","name":"fixture"});err!=nil {t.Fatal(err)}
 if _,err:=st.AppendEvent(ctx,store.Event{ID:"old",Type:"approval.upsert",AgentID:"a",SessionID:"s",Data:map[string]any{"id":"old","state":"pending"}});err!=nil {t.Fatal(err)}
 if err:=h.refreshHermesApprovals(ctx,map[string]any{"agent_id":"a","id":"s","source_session_id":"native"});err!=nil {t.Fatal(err)}
 remaining,err:=st.PendingApprovals(ctx)
 if !called || err!=nil || len(remaining)!=0 {t.Fatalf("refresh failed: called=%v approvals=%v err=%v",called,remaining,err)}
}
