
 = [System.Environment]::GetEnvironmentVariable('ASTRORDER_CLIP_FILES')
if () {
    Add-Type -AssemblyName System.Windows.Forms
     = New-Object System.Collections.Specialized.StringCollection
    foreach ( in ( -split ';')) {
         = .Trim()
        if () { .Add() }
    }
    if (.Count -gt 0) {
         = New-Object System.Windows.Forms.DataObject
        .SetFileDropList()
        [System.Windows.Forms.Clipboard]::SetDataObject(, True)
        Write-Output 'OK'
    }
}
