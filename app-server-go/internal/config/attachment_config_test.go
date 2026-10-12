package config

import "testing"

func TestLoadAttachmentPolicy(t *testing.T){
 t.Setenv("ASTRORDER_MAX_ATTACHMENT_SIZE","1234")
 t.Setenv("ASTRORDER_ALLOWED_ATTACHMENT_TYPES","text/plain,image/png")
 cfg,err:=Load("");if err!=nil{t.Fatal(err)}
 if cfg.MaxAttachmentSize!=1234||len(cfg.AllowedAttachmentTypes)!=2||cfg.AllowedAttachmentTypes[0]!="text/plain"{t.Fatal("attachment policy not preserved")}
 t.Setenv("ASTRORDER_MAX_ATTACHMENT_SIZE","0");if _,err:=Load("");err==nil{t.Fatal("zero size accepted")}
}
