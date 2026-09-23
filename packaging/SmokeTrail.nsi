;  SmokeTrail installer.
;
;  NSIS rather than Inno Setup for one reason: makensis runs on Linux, so the
;  whole release — binaries and installer — comes out of a single ubuntu job with
;  no Windows runner in the loop. That matters more than Inno's nicer default
;  wizard, and it keeps the release pipeline honest with ADR 1: one toolchain, no
;  per-platform build machines.
;
;  The installer itself does almost nothing. It lays down one .exe and calls
;  `SmokeTrail.exe install`, which is the same code path an administrator runs
;  from a prompt. One implementation of "become a service", exercised by both
;  routes, so the graphical path cannot rot while the command-line one stays
;  tested.
;
;  It deliberately does NOT create a data folder beside the .exe — that absence is
;  what makes an installed copy use %ProgramData%. See ADR 5.
;
;    makensis -DVERSION=0.1.0 -DSRCEXE=../dist/SmokeTrail.exe SmokeTrail.nsi

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef SRCEXE
  !define SRCEXE "..\dist\SmokeTrail.exe"
!endif
!ifndef OUTFILE
  !define OUTFILE "..\dist\SmokeTrail-${VERSION}-setup.exe"
!endif

!define APPNAME   "SmokeTrail"
!define PUBLISHER "githubflyideas"
!define HOMEPAGE  "https://github.com/githubflyideas/SmokeTrail"
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
VIAddVersionKey "LegalCopyright"  "Copyright (c) 2026 ${PUBLISHER}. MIT licensed."

!include "MUI2.nsh"
!include "nsDialogs.nsh"
!include "LogicLib.nsh"

!define MUI_ICON   "icon\SmokeTrail.ico"
!define MUI_UNICON "icon\SmokeTrail.ico"
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
!define MUI_FINISHPAGE_LINK "SmokeTrail on GitHub"
!define MUI_FINISHPAGE_LINK_LOCATION "${HOMEPAGE}"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "Japanese"
!insertmacro MUI_LANGUAGE "SimpChinese"

Var PortDialog
Var PortField
Var Port

Function .onInit
  StrCpy $Port "8518"
  !insertmacro MUI_LANGDLL_DISPLAY
FunctionEnd

Function PortPageCreate
  !insertmacro MUI_HEADER_TEXT "Console port" \
    "SmokeTrail has no window of its own - you manage it in a browser."
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

Section "SmokeTrail" SecMain
  SectionIn RO
  SetOutPath "$INSTDIR"
  File /oname=SmokeTrail.exe "${SRCEXE}"
  File "..\README.md"

  WriteRegStr HKLM "Software\${APPNAME}" "InstallDir" "$INSTDIR"
  WriteRegStr HKLM "Software\${APPNAME}" "Port" "$Port"

  ; Add/Remove Programs. EstimatedSize keeps the list from showing a blank size.
  WriteRegStr   HKLM "${REGKEY}" "DisplayName"     "${APPNAME}"
  WriteRegStr   HKLM "${REGKEY}" "DisplayVersion"  "${VERSION}"
  WriteRegStr   HKLM "${REGKEY}" "Publisher"       "${PUBLISHER}"
  WriteRegStr   HKLM "${REGKEY}" "DisplayIcon"     "$INSTDIR\SmokeTrail.exe"
  WriteRegStr   HKLM "${REGKEY}" "URLInfoAbout"    "${HOMEPAGE}"
  WriteRegStr   HKLM "${REGKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr   HKLM "${REGKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr   HKLM "${REGKEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${REGKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${REGKEY}" "NoRepair" 1
  WriteRegDWORD HKLM "${REGKEY}" "EstimatedSize" 12000

  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateDirectory "$SMPROGRAMS\${APPNAME}"
  CreateShortCut "$SMPROGRAMS\${APPNAME}\${APPNAME} console.lnk" \
    "$INSTDIR\SmokeTrail.exe" "" "$INSTDIR\SmokeTrail.exe" 0
  CreateShortCut "$SMPROGRAMS\${APPNAME}\Uninstall ${APPNAME}.lnk" "$INSTDIR\uninstall.exe"

  ; The service registration itself. We are already elevated, so the exe's own
  ; self-elevation path is skipped and this runs straight through.
  DetailPrint "Registering the SmokeTrail service..."
  nsExec::ExecToLog '"$INSTDIR\SmokeTrail.exe" install --port $Port'
  Pop $0
  ${If} $0 != 0
    MessageBox MB_ICONSTOP|MB_OK \
      "The files were installed, but the SmokeTrail service could not be registered (exit $0).$\n$\nRun this from an elevated prompt to see why:$\n  $INSTDIR\SmokeTrail.exe install --port $Port"
  ${EndIf}
SectionEnd

Section "Uninstall"
  ; Stop and deregister before deleting the binary that knows how to do it.
  nsExec::ExecToLog '"$INSTDIR\SmokeTrail.exe" uninstall'
  Pop $0

  Delete "$INSTDIR\SmokeTrail.exe"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

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
