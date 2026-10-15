; Astrorder Wails 3 NSIS Installer Script
Unicode True

!define PRODUCT_NAME "Astrorder"
!define PRODUCT_VERSION "0.1.0"
!define PRODUCT_PUBLISHER "Astrorder"
!define PRODUCT_DIR_REGKEY "Software\Microsoft\Windows\CurrentVersion\App Paths\Astrorder.exe"
!define PRODUCT_UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}"

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

Function .onInit
  ${GetParameters} $R0
  ClearErrors
  ${GetOptions} $R0 "/ISOLATED" $R1
  IfErrors normal_install
  StrCpy $IsolatedInstall "1"
normal_install:
  IfSilent skip_webview_check
  ; Check WebView2 runtime across HKLM and HKCU
  SetRegView 64
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BAF-6D3A070E2837}" "pv"
  StrCmp $0 "" 0 skip_webview_check
  ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BAF-6D3A070E2837}" "pv"
  StrCmp $0 "" 0 skip_webview_check
  ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BAF-6D3A070E2837}" "pv"
  StrCmp $0 "" 0 skip_webview_check
  SetRegView 32
  ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BAF-6D3A070E2837}" "pv"
  StrCmp $0 "" 0 skip_webview_check
  MessageBox MB_OK|MB_ICONEXCLAMATION "未检测到 Microsoft Edge WebView2 运行时。请确保系统已安装 WebView2，否则 Astrorder 可能无法正常运行。"
skip_webview_check:
  SetRegView default
FunctionEnd

Section "MainSection" SEC01
  SetOutPath "$INSTDIR"
  SetOverwrite on

  ; Recreate the desktop file: overwriting keeps its old NTFS identity and
  ; Explorer can retain the legacy shell's default document icon.
  ; A running desktop must be closed; never delete on reboot or touch kernels.
  IfFileExists "$INSTDIR\Astrorder.exe" 0 desktop_file_ready
  ClearErrors
  Delete "$INSTDIR\Astrorder.exe"
  IfErrors desktop_file_in_use
  Goto desktop_file_ready
desktop_file_in_use:
  Abort "请先退出 Astrorder 客户端后重试安装。后台会话服务无需停止。"
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
