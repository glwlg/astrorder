package store

import (
 "context"
 "database/sql"
 "encoding/json"
 "errors"
)

func nativeTurnKey(agentID, sessionID string) string {
 encoded,_:=json.Marshal([]string{agentID,sessionID})
 return "native_turn:"+string(encoded)
}

func NativeTurnCommandKey(agentID, sessionID string) string {
	encoded, _ := json.Marshal([]string{agentID, sessionID})
	return "native_turn_command:" + string(encoded)
}

// ActiveTurn returns the durable exact native turn ID for one Agent/session.
func(s *Store)ActiveTurn(ctx context.Context,agentID,sessionID string)(string,error){
 value,err:=s.GetPreference(ctx,nativeTurnKey(agentID,sessionID));if err!=nil||!value.Exists||value.Value==nil{return "",err}
 turn,ok:=value.Value.(string);if !ok{return "",errors.New("Invalid stored native turn identity")};return turn,nil
}

// SetActiveTurn persists a native turn observed outside event projection.
func(s *Store)SetActiveTurn(ctx context.Context,agentID,sessionID,turnID string)error{
 if agentID==""||sessionID==""||turnID==""{return errors.New("Native turn identity is required")}
 return s.SetPreference(ctx,nativeTurnKey(agentID,sessionID),turnID,true)
}

// RecordAcceptedTurn does not resurrect a turn completed before its RPC reply.
func(s *Store)RecordAcceptedTurn(ctx context.Context,agentID,sessionID,commandID,turnID string)error{
 return s.WithTx(ctx,func(tx *sql.Tx)error{
  rows,err:=queryRows(ctx,tx,"SELECT state FROM commands WHERE agent_id=? AND session_id=? AND id=?",agentID,sessionID,commandID);if err!=nil{return err};if len(rows)!=1{return errNotFound}
  switch rows[0]["state"]{case "completed","cancelled","failed":return nil}
  encoded,err:=encodeJSON(turnID);if err!=nil{return err}
  _,err=tx.ExecContext(ctx,`INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`,nativeTurnKey(agentID,sessionID),encoded);return err
 })
}
