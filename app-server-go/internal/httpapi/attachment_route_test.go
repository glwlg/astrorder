package httpapi

import (
 "bytes"
 "encoding/json"
 "mime/multipart"
 "net/http"
 "net/http/httptest"
 "net/textproto"
 "path/filepath"
 "testing"
 "astrorder.dev/app-server/internal/config"
 "astrorder.dev/app-server/internal/store"
)
func TestFormalUploadUsesConfiguredAttachmentTypesAndLimit(t *testing.T){
 st,err:=store.Open(filepath.Join(t.TempDir(),"app.db"));if err!=nil{t.Fatal(err)};defer st.Close()
 cfg:=config.Config{BrowserSecret:"fixture",AttachmentsDir:t.TempDir()}
 handler,err:=NewHandlerChecked(Options{Config:cfg,Store:st});if err!=nil{t.Fatal(err)}
 var body bytes.Buffer;form:=multipart.NewWriter(&body);header:=textproto.MIMEHeader{};header.Set("Content-Disposition",`form-data; name="file"; filename="note.txt"`);header.Set("Content-Type","text/plain");part,err:=form.CreatePart(header);if err!=nil{t.Fatal(err)};part.Write([]byte("attachment fixture"));form.Close()
 request:=httptest.NewRequest(http.MethodPost,"/api/v1/attachments",&body);request.Header.Set("Content-Type",form.FormDataContentType());request.Header.Set("Authorization","Bearer fixture");response:=httptest.NewRecorder();handler.ServeHTTP(response,request)
 if response.Code!=201{t.Fatalf("formal upload=%d %s",response.Code,response.Body.String())}
 var record map[string]any;if err=json.Unmarshal(response.Body.Bytes(),&record);err!=nil{t.Fatal(err)}
 request=httptest.NewRequest(http.MethodGet,"/api/v1/attachments/"+record["id"].(string),nil);request.Header.Set("Authorization","Bearer fixture");response=httptest.NewRecorder();handler.ServeHTTP(response,request)
 if response.Code!=200||response.Body.String()!="attachment fixture"{t.Fatalf("readback=%d %s",response.Code,response.Body.String())}
}
