Option Explicit

Dim ws, fso, scriptDir, rootDir, pythonwExe, pythonExe, cmd, logFile

Set ws = CreateObject("WScript.Shell")
Set fso = CreateObject("Scripting.FileSystemObject")

scriptDir = fso.GetParentFolderName(WScript.ScriptFullName)
rootDir = fso.GetParentFolderName(scriptDir)
logFile = rootDir & "\.runtime\vbs_debug.log"

pythonwExe = rootDir & "\backend\.venv\Scripts\pythonw.exe"

cmd = """" & pythonwExe & """ """ & rootDir & "\scripts\start_silent.py"""

ws.CurrentDirectory = rootDir
ws.Run cmd, 0, False
