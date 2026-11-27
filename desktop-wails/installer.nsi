; Astrorder Wails 3 NSIS Installer Script
Unicode True

!define PRODUCT_NAME "Astrorder"
!define PRODUCT_VERSION "0.1.0"
!define PRODUCT_PUBLISHER "Astrorder"
!define PRODUCT_DIR_REGKEY "Software\Microsoft\Windows\CurrentVersion\App Paths\Astrorder.exe"
!define PRODUCT_UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}"
!define WEBVIEW2_BOOTSTRAPPER_URL "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

!include "MUI2.nsh"
!include "FileFunc.nsh"

; MUI Settings
!define MUI_ABORTWARNING
!define MUI_ICON "assets\app.ico"
!define MUI_UNICON "assets\app.ico"

; Installer Pages
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

; Uninstaller Pages
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Name "${PRODUCT_NAME} ${PRODUCT_VERSION}"
OutFile "..\release\Astrorder-Setup-${PRODUCT_VERSION}.exe"
InstallDir "$LOCALAPPDATA\Programs\Astrorder"
InstallDirRegKey HKCU "${PRODUCT_DIR_REGKEY}" ""
RequestExecutionLevel user
Var IsolatedInstall
Var WebView2Installed
Var ShouldInstallWebView2

; Check WebView2 Evergreen Runtime across 64-bit and 32-bit HKLM/HKCU registry hives.
Function CheckWebView2Installed
  StrCpy $WebView2Installed "0"
  SetRegView 64
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BAF-6D3A070E2837}" "pv"
  StrCmp $0 "" 0 webview2_found
  ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BAF-6D3A070E2837}" "pv"
  StrCmp $0 "" 0 webview2_found
  ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BAF-6D3A070E2837}" "pv"
  StrCmp $0 "" 0 webview2_found
  SetRegView 32
  ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BAF-6D3A070E2837}" "pv"
  StrCmp $0 "" 0 webview2_found
  ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BAF-6D3A070E2837}" "pv"
  StrCmp $0 "" 0 webview2_found
  Goto webview2_done
webview2_found:
  StrCpy $WebView2Installed "1"
webview2_done:
  SetRegView default
FunctionEnd

; Download and run Microsoft Edge WebView2 Evergreen Bootstrapper (silent first,
; interactive fallback in UI mode). Never touches user data or Astrorder kernels.
Function InstallWebView2Bootstrapper
  InitPluginsDir
  DetailPrint "正在下载 Microsoft Edge WebView2 运行时引导程序…"
  StrCpy $R2 "$PLUGINSDIR\MicrosoftEdgeWebview2Setup.exe"

  ; Try built-in Windows curl.exe first, then fall back to PowerShell TLS 1.2.
  nsExec::ExecToLog '"$SYSDIR\curl.exe" -fsSL --connect-timeout 20 -o "$R2" "${WEBVIEW2_BOOTSTRAPPER_URL}"'
  Pop $0
  StrCmp $0 "0" check_downloaded
  System::Call 'kernel32::SetEnvironmentVariable(t "ASTRORDER_WV2_URL", t "${WEBVIEW2_BOOTSTRAPPER_URL}")'
  System::Call 'kernel32::SetEnvironmentVariable(t "ASTRORDER_WV2_OUT", t "$R2")'
  nsExec::ExecToLog "powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command $\"[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; Invoke-WebRequest -UseBasicParsing -Uri $$env:ASTRORDER_WV2_URL -OutFile $$env:ASTRORDER_WV2_OUT$\""
  Pop $0

check_downloaded:
  IfFileExists "$R2" run_bootstrapper download_failed

run_bootstrapper:
  DetailPrint "正在安装 Microsoft Edge WebView2 运行时（静默模式）…"
  ExecWait '"$R2" /silent /install' $1
  Call CheckWebView2Installed
  StrCmp $WebView2Installed "1" install_ok

  ; Interactive fallback when not in silent installer mode.
  IfSilent install_incomplete
  DetailPrint "静默安装未完成（退出码 $1），尝试启动交互式 WebView2 安装向导…"
  ExecWait '"$R2"' $1
  Call CheckWebView2Installed
  StrCmp $WebView2Installed "1" install_ok

install_incomplete:
  DetailPrint "WebView2 运行时尚未确认安装完成（退出码 $1）。"
  IfSilent install_done
  MessageBox MB_OK|MB_ICONEXCLAMATION "Microsoft Edge WebView2 运行时未能自动完成安装（退出码 $1）。Astrorder 将继续完成安装，但首次启动前可能需要手动安装 WebView2（https://developer.microsoft.com/microsoft-edge/webview2/）。"
  Goto install_done

download_failed:
  DetailPrint "下载 Microsoft Edge WebView2 引导程序失败。"
  IfSilent install_done
  MessageBox MB_OK|MB_ICONEXCLAMATION "无法自动下载 Microsoft Edge WebView2 运行时（请检查网络连接）。Astrorder 将继续完成安装，若启动界面空白请手动安装 WebView2：https://developer.microsoft.com/microsoft-edge/webview2/"
  Goto install_done

install_ok:
  DetailPrint "Microsoft Edge WebView2 运行时安装成功。"
install_done:
  Delete "$R2"
FunctionEnd

; Gracefully close only the old desktop UI (Astrorder.exe in $INSTDIR) FIRST,
; then stop the big kernel (astrorder-app.exe via installed astrorderctl.exe).
; Each check runs independently so a missing Astrorder.exe never skips stopping
; an existing astrorder-app.exe.
; Strictly obeys AGENTS.md: NEVER stop, kill, or touch astrorder-sessiond.exe!
Function GracefulCloseExistingDesktopAndApp
  ; 1. Gracefully close only Astrorder.exe launched from $INSTDIR (prevents tray from restarting app).
  IfFileExists "$INSTDIR\Astrorder.exe" 0 stop_big_kernel
  DetailPrint "正在关闭旧版星序桌面窗口…"
  System::Call 'kernel32::SetEnvironmentVariable(t "ASTRORDER_TARGET_EXE", t "$INSTDIR\Astrorder.exe")'
  nsExec::ExecToLog "powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command $\"$$target = [System.IO.Path]::GetFullPath($$env:ASTRORDER_TARGET_EXE); $$procs = Get-Process -Name Astrorder -ErrorAction SilentlyContinue | Where-Object { try { $$_.Path -and ([System.IO.Path]::GetFullPath($$_.Path) -ieq $$target) } catch { $$false } }; foreach ($$p in $$procs) { try { $$null = $$p.CloseMainWindow() } catch {} }; Start-Sleep -Milliseconds 1200; foreach ($$p in $$procs) { if (-not $$p.HasExited) { try { Stop-Process -Id $$p.Id -ErrorAction SilentlyContinue } catch {} } }; Start-Sleep -Milliseconds 600$\""
  Pop $0

stop_big_kernel:
  ; 2. Stop big kernel gracefully via installed astrorderctl.exe (never touches small kernel).
  IfFileExists "$INSTDIR\server\bin\astrorderctl.exe" 0 graceful_close_done
  DetailPrint "正在优雅停止旧版主服务（后台会话服务保持运行）…"
  nsExec::ExecToLog '"$INSTDIR\server\bin\astrorderctl.exe" service app stop --root "$INSTDIR\server" --state-dir "$LOCALAPPDATA\Astrorder"'
  Pop $0

graceful_close_done:
FunctionEnd

Function .onInit
  StrCpy $IsolatedInstall "0"
  StrCpy $ShouldInstallWebView2 "0"
  ${GetParameters} $R0
  ClearErrors
  ${GetOptions} $R0 "/ISOLATED" $R1
  IfErrors normal_install
  StrCpy $IsolatedInstall "1"
normal_install:
  ; Isolated verification runs skip external network WebView2 bootstrapper.
  StrCmp $IsolatedInstall "1" skip_webview_check
  Call CheckWebView2Installed
  StrCmp $WebView2Installed "1" skip_webview_check
  IfSilent silent_webview_auto
  MessageBox MB_YESNO|MB_ICONINFORMATION "未检测到 Microsoft Edge WebView2 运行时（星序桌面界面必需组件）。$\r$\n$\r$\n是否现在自动下载并安装 WebView2 运行时？" IDYES enable_webview_install IDNO skip_webview_check
silent_webview_auto:
enable_webview_install:
  StrCpy $ShouldInstallWebView2 "1"
skip_webview_check:
FunctionEnd

Section "MainSection" SEC01
  SetOutPath "$INSTDIR"
  SetOverwrite on

  ; Auto-install WebView2 runtime if missing and approved/silent (non-isolated).
  StrCmp $ShouldInstallWebView2 "1" 0 webview2_ready
  Call InstallWebView2Bootstrapper
webview2_ready:

  ; On non-isolated overwrite upgrades, gracefully stop old desktop + big kernel
  ; before replacing files (even if Astrorder.exe is missing while astrorder-app.exe
  ; still runs). Never touch or stop astrorder-sessiond.exe!
  StrCmp $IsolatedInstall "1" skip_graceful_close
  Call GracefulCloseExistingDesktopAndApp
skip_graceful_close:

  ; Recreate the desktop file: overwriting keeps its old NTFS identity and
  ; Explorer can retain the legacy shell's default document icon.
  IfFileExists "$INSTDIR\Astrorder.exe" 0 desktop_file_ready
  ClearErrors
  Delete "$INSTDIR\Astrorder.exe"
  IfErrors retry_delete_desktop
  Goto desktop_file_ready
retry_delete_desktop:
  Sleep 1000
  ClearErrors
  Delete "$INSTDIR\Astrorder.exe"
  IfErrors desktop_file_in_use
  Goto desktop_file_ready
desktop_file_in_use:
  Abort "请先从系统托盘退出旧版 Astrorder 桌面窗口后重试安装。后台会话服务（小内核）无需停止。"
desktop_file_ready:

  ; Existing installations retain their daemon until the tray updater runs.
  ; Fresh installations receive the daemon directly.
  SetOutPath "$INSTDIR\server\bin"
  IfFileExists "$INSTDIR\server\bin\astrorder-sessiond.exe" stage_daemon fresh_daemon
stage_daemon:
  File /oname=astrorder-sessiond.exe.new "build-package\server\bin\astrorder-sessiond.exe"
  Goto daemon_ready
fresh_daemon:
  File "build-package\server\bin\astrorder-sessiond.exe"
daemon_ready:
  SetOutPath "$INSTDIR"
  File /r /x astrorder-sessiond.exe "build-package\*.*"

  ; Create uninstaller
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; Isolation acceptance must not overwrite the real installation registration.
  StrCmp $IsolatedInstall "1" isolated_registration

  ; Create Shortcuts
  CreateDirectory "$SMPROGRAMS\Astrorder"
  CreateShortcut "$SMPROGRAMS\Astrorder\Astrorder.lnk" "$INSTDIR\Astrorder.exe" "--packaged" "$INSTDIR\Astrorder.exe" 0
  CreateShortcut "$SMPROGRAMS\Astrorder\卸载 Astrorder.lnk" "$INSTDIR\uninstall.exe" "" "$INSTDIR\uninstall.exe" 0
  CreateShortcut "$DESKTOP\Astrorder.lnk" "$INSTDIR\Astrorder.exe" "--packaged" "$INSTDIR\Astrorder.exe" 0

  ; Write Registry for Uninstall
  WriteRegStr HKCU "${PRODUCT_DIR_REGKEY}" "" "$INSTDIR\Astrorder.exe"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "UninstallString" "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "DisplayIcon" "$INSTDIR\Astrorder.exe,0"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "Publisher" "${PRODUCT_PUBLISHER}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "InstallLocation" "$INSTDIR"
  Goto registration_done
isolated_registration:
  FileOpen $R0 "$INSTDIR\.isolated-install" w
  FileClose $R0
registration_done:
SectionEnd

Section "Uninstall"
  IfFileExists "$INSTDIR\.isolated-install" remove_files
  ; Remove Shortcuts
  Delete "$DESKTOP\Astrorder.lnk"
  Delete "$SMPROGRAMS\Astrorder\Astrorder.lnk"
  Delete "$SMPROGRAMS\Astrorder\卸载 Astrorder.lnk"
  RMDir "$SMPROGRAMS\Astrorder"

  ; Remove Registry
  DeleteRegKey HKCU "${PRODUCT_DIR_REGKEY}"
  DeleteRegKey HKCU "${PRODUCT_UNINST_KEY}"

remove_files:
  Delete "$INSTDIR\.isolated-install"
  ; Remove Installed Files (Keep user data in LocalAppData\Astrorder)
  RMDir /r "$INSTDIR\server"
  Delete "$INSTDIR\Astrorder.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  SetAutoClose true
SectionEnd
