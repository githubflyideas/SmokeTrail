;  pingping installer.
;
;  NSIS rather than Inno Setup for one reason: makensis runs on Linux, so the
;  whole release — binaries and installer — comes out of a single ubuntu job with
;  no Windows runner in the loop. That matters more than Inno's nicer default
;  wizard, and it keeps the release pipeline honest with ADR 1: one toolchain, no
;  per-platform build machines.
;
;  The installer itself does almost nothing. It lays down one .exe and calls
;  `pingping.exe install`, which is the same code path an administrator runs
;  from a prompt. One implementation of "become a service", exercised by both
;  routes, so the graphical path cannot rot while the command-line one stays
;  tested.
;
;  It deliberately does NOT create a data folder beside the .exe — that absence is
;  what makes an installed copy use %ProgramData%. See ADR 5.
;
;    makensis -DVERSION=0.1.0 -DSRCEXE=../dist/pingping.exe pingping.nsi

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef SRCEXE
  !define SRCEXE "..\dist\pingping.exe"
!endif
!ifndef OUTFILE
  !define OUTFILE "..\dist\pingping-${VERSION}-setup.exe"
!endif

!define APPNAME   "pingping"
!define PUBLISHER "githubflyideas"
!define HOMEPAGE  "https://github.com/githubflyideas/pingping"
!define REGKEY    "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}"

Name "${APPNAME} ${VERSION}"
OutFile "${OUTFILE}"
Unicode True
InstallDir "$PROGRAMFILES64\${APPNAME}"
InstallDirRegKey HKLM "Software\${APPNAME}" "InstallDir"
; Registering a service and touching the firewall both need it.
RequestExecutionLevel admin
SetCompressor /SOLID lzma

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName"     "${APPNAME}"
VIAddVersionKey "CompanyName"     "${PUBLISHER}"
VIAddVersionKey "FileDescription" "${APPNAME} installer"
VIAddVersionKey "FileVersion"     "${VERSION}.0"
VIAddVersionKey "ProductVersion"  "${VERSION}.0"
VIAddVersionKey "LegalCopyright"  "Copyright 2026 ${PUBLISHER}. Apache License 2.0."

!include "MUI2.nsh"
!include "nsDialogs.nsh"
!include "LogicLib.nsh"

!define MUI_ICON   "icon\pingping.ico"
!define MUI_UNICON "icon\pingping.ico"
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_LICENSE "..\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
Page custom PortPageCreate PortPageLeave
!insertmacro MUI_PAGE_INSTFILES

; First run has no password yet, so sending the operator to the console straight
; away is not a nicety — it is how the admin account gets created before anyone
; else on the network can claim it. See ADR 4.
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_FUNCTION OpenConsole
!define MUI_FINISHPAGE_RUN_TEXT "Open the console and create the admin account"
!define MUI_FINISHPAGE_LINK "pingping on GitHub"
!define MUI_FINISHPAGE_LINK_LOCATION "${HOMEPAGE}"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

; The same ten the console and the notification icon speak, in the same order.
; An installer that offers three while the program offers ten tells the user the
; translation is partial before they have even seen it.
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "Spanish"
!insertmacro MUI_LANGUAGE "French"
!insertmacro MUI_LANGUAGE "PortugueseBR"
!insertmacro MUI_LANGUAGE "Russian"
!insertmacro MUI_LANGUAGE "Indonesian"
!insertmacro MUI_LANGUAGE "German"
!insertmacro MUI_LANGUAGE "Japanese"
!insertmacro MUI_LANGUAGE "Korean"

Var PortDialog
Var PortField
Var Port

Function .onInit
  StrCpy $Port "8518"
  !insertmacro MUI_LANGDLL_DISPLAY
FunctionEnd

Function PortPageCreate
  !insertmacro MUI_HEADER_TEXT "Console port" \
    "pingping has no window of its own - you manage it in a browser."
  nsDialogs::Create 1018
  Pop $PortDialog
  ${If} $PortDialog == error
    Abort
  ${EndIf}
  ${NSD_CreateLabel} 0 0 100% 34u \
    "Choose the port for the web console. The installer will open it in Windows Firewall on the private and domain profiles."
  Pop $0
  ${NSD_CreateLabel} 0 44u 40u 12u "Port:"
  Pop $0
  ${NSD_CreateNumber} 42u 42u 60u 12u "$Port"
  Pop $PortField
  nsDialogs::Show
FunctionEnd

Function PortPageLeave
  ${NSD_GetText} $PortField $Port
  ${If} $Port < 1
  ${OrIf} $Port > 65535
    MessageBox MB_ICONEXCLAMATION|MB_OK "Enter a port between 1 and 65535."
    Abort
  ${EndIf}
FunctionEnd

Function OpenConsole
  ExecShell "open" "http://localhost:$Port/"
FunctionEnd

Section "pingping" SecMain
  SectionIn RO

  ; Stop whatever is already running before touching the files. Windows will not
  ; let anyone overwrite a running executable, so without this an upgrade fails
  ; with "error opening file for writing" on pingping.exe and leaves the install
  ; half done. The service holds the exe, and so does every notification-area
  ; process — `sc stop` asks the first, taskkill takes the rest, and the pause
  ; gives Windows time to release the handles before the copy starts.
  ; Stop cleanly first, and give it time to finish. Force-killing the service
  ; process is a crash as far as the SCM is concerned, and this service is
  ; configured to be restarted after one — so the blunt version triggered the
  ; recovery action and then raced it, which is how an install that worked
  ; reported that the service would not start. A clean stop fires no recovery.
  ; The taskkill afterwards is for the notification-area processes, which hold
  ; the exe but are not services and will not stop on their own.
  DetailPrint "Stopping any running copy of pingping..."
  nsExec::ExecToLog 'sc stop pingping'
  Pop $0
  Sleep 4000
  nsExec::ExecToLog 'taskkill /F /IM pingping.exe'
  Pop $0
  Sleep 1500

  SetOutPath "$INSTDIR"
  File /oname=pingping.exe "${SRCEXE}"
  File "..\README.md"

  ; Port and DataDir are written by `pingping.exe install` itself, as the
  ; values the tray process reads. One writer, one type.
  WriteRegStr HKLM "Software\${APPNAME}" "InstallDir" "$INSTDIR"

  ; Add/Remove Programs. EstimatedSize keeps the list from showing a blank size.
  WriteRegStr   HKLM "${REGKEY}" "DisplayName"     "${APPNAME}"
  WriteRegStr   HKLM "${REGKEY}" "DisplayVersion"  "${VERSION}"
  WriteRegStr   HKLM "${REGKEY}" "Publisher"       "${PUBLISHER}"
  WriteRegStr   HKLM "${REGKEY}" "DisplayIcon"     "$INSTDIR\pingping.exe"
  WriteRegStr   HKLM "${REGKEY}" "URLInfoAbout"    "${HOMEPAGE}"
  WriteRegStr   HKLM "${REGKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr   HKLM "${REGKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr   HKLM "${REGKEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${REGKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${REGKEY}" "NoRepair" 1
  WriteRegDWORD HKLM "${REGKEY}" "EstimatedSize" 12000

  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; All-users Startup, so the icon comes back at every sign-in. A service has no
  ; window; without this the answer to "it is installed, now what?" is "remember
  ; a port number and type it into a browser".
  SetShellVarContext all
  CreateShortCut "$SMSTARTUP\${APPNAME} tray.lnk" \
    "$INSTDIR\pingping.exe" "tray" "$INSTDIR\pingping.exe" 0

  CreateDirectory "$SMPROGRAMS\${APPNAME}"
  CreateShortCut "$SMPROGRAMS\${APPNAME}\${APPNAME} console.lnk" \
    "$INSTDIR\pingping.exe" "" "$INSTDIR\pingping.exe" 0
  CreateShortCut "$SMPROGRAMS\${APPNAME}\Uninstall ${APPNAME}.lnk" "$INSTDIR\uninstall.exe"

  ; The service registration itself. We are already elevated, so the exe's own
  ; self-elevation path is skipped and this runs straight through.
  ; ExecToStack, not ExecToLog: the installer already HAS the reason this
  ; failed, and the old dialog threw it away and told the operator to go and run
  ; a command to find out — a round trip, on the one machine where the fault is
  ; reproducible, for a string that was in $1 the whole time. It goes to the
  ; details pane as well, via DetailPrint, so both places have it.
  DetailPrint "Registering the pingping service..."
  nsExec::ExecToStack '"$INSTDIR\pingping.exe" install --port $Port'
  Pop $0   ; exit code
  Pop $1   ; captured output
  DetailPrint "$1"
  ; Exit 11 means the service exists and something after that went wrong. Saying
  ; "could not be registered" for a registered service sent every attempt to
  ; diagnose this at the wrong step, for three releases.
  ${If} $0 == 11
    MessageBox MB_ICONEXCLAMATION|MB_OK \
      "pingping is installed and the service is registered, but it did not start:$\n$\n$1$\nStart it with:$\n  sc start pingping"
  ${ElseIf} $0 != 0
    MessageBox MB_ICONSTOP|MB_OK \
      "The files were installed, but the pingping service could not be registered (exit $0).$\n$\n$1$\nYou can retry with:$\n  $INSTDIR\pingping.exe install --port $Port"
  ${EndIf}

  ; Start the tray now rather than making the operator sign out and back in.
  ; It waits for the service to answer before it shows anything.
  Exec '"$INSTDIR\pingping.exe" tray'
SectionEnd

Section "Uninstall"
  SetShellVarContext all

  ; Deregister first — that stops the service cleanly, and it needs the binary
  ; we are about to delete. Only then kill whatever is left, which is the tray
  ; process holding the exe open.
  nsExec::ExecToLog '"$INSTDIR\pingping.exe" uninstall'
  Pop $0
  nsExec::ExecToLog 'taskkill /F /IM pingping.exe'
  Pop $0
  Sleep 800

  Delete "$INSTDIR\pingping.exe"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  Delete "$SMSTARTUP\${APPNAME} tray.lnk"
  Delete "$SMPROGRAMS\${APPNAME}\${APPNAME} console.lnk"
  Delete "$SMPROGRAMS\${APPNAME}\Uninstall ${APPNAME}.lnk"
  RMDir "$SMPROGRAMS\${APPNAME}"

  DeleteRegKey HKLM "${REGKEY}"
  DeleteRegKey HKLM "Software\${APPNAME}"

  ; The collected history is left behind unless the operator asks for it to go.
  ; Removing the program is not a request to discard months of measurements.
  IfSilent done
  IfFileExists "$COMMONPROGRAMDATA\${APPNAME}\*.*" 0 done
    MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 \
      "Delete the collected history in$\n$COMMONPROGRAMDATA\${APPNAME}?$\n$\nChoose No to keep it for a future install." \
      IDNO keep
    RMDir /r "$COMMONPROGRAMDATA\${APPNAME}"
    Goto done
  keep:
    DetailPrint "History kept in $COMMONPROGRAMDATA\${APPNAME}"
  done:
SectionEnd
