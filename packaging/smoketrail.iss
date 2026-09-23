; Inno Setup script for SmokeTrail.
;
; The installer deliberately does almost nothing itself: it lays down one .exe and
; then calls `smoketrail.exe install`, which is the same code path an administrator
; runs from a prompt. One implementation of "become a service", exercised by both
; routes, so the graphical path cannot rot while the command-line one is tested.
;
; It also does NOT create a data directory beside the .exe — that absence is what
; makes an installed copy use %ProgramData% instead of portable mode. See ADR 5.
;
;   iscc packaging\smoketrail.iss /DAppVersion=0.1.0 /DSourceExe=..\dist\SmokeTrail-windows-amd64.exe

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef SourceExe
  #define SourceExe "..\dist\SmokeTrail-windows-amd64.exe"
#endif

[Setup]
AppId={{6F3A1C64-8E5B-4D2A-9C77-5B1E0A7D33F1}
AppName=SmokeTrail
AppVersion={#AppVersion}
AppPublisher=githubflyideas
AppPublisherURL=https://github.com/githubflyideas/SmokeTrail
AppSupportURL=https://github.com/githubflyideas/SmokeTrail/issues
DefaultDirName={autopf}\SmokeTrail
DefaultGroupName=SmokeTrail
UninstallDisplayName=SmokeTrail
UninstallDisplayIcon={app}\SmokeTrail.exe
SetupIconFile=icon\SmokeTrail.ico
VersionInfoVersion={#AppVersion}
VersionInfoProductName=SmokeTrail
VersionInfoCompany=githubflyideas
OutputDir=..\dist
OutputBaseFilename=SmokeTrail-{#AppVersion}-setup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
; install/uninstall register a service and touch the firewall.
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
ArchitecturesAllowed=x64compatible
DisableProgramGroupPage=yes
LicenseFile=..\LICENSE

[Languages]
Name: "en"; MessagesFile: "compiler:Default.isl"
Name: "ja"; MessagesFile: "compiler:Languages\Japanese.isl"
Name: "zh"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"

[Files]
Source: "{#SourceExe}"; DestName: "SmokeTrail.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md";  DestDir: "{app}"; Flags: ignoreversion isreadme

[Icons]
Name: "{group}\SmokeTrail console"; Filename: "http://localhost:{code:GetPort}/"
Name: "{group}\Uninstall SmokeTrail"; Filename: "{uninstallexe}"

[Run]
; The service registration itself. Failure is surfaced rather than swallowed: an
; install that silently left no service behind is the worst possible outcome.
Filename: "{app}\SmokeTrail.exe"; Parameters: "install --port {code:GetPort}"; \
  StatusMsg: "Registering the SmokeTrail service..."; Flags: runhidden waituntilterminated

; First run has no password yet, so sending the operator to the console straight
; away is not a nicety — it is how the account gets created before anyone else on
; the network can claim it. See ADR 4.
Filename: "http://localhost:{code:GetPort}/"; \
  Description: "Open the console and create the admin account"; \
  Flags: postinstall shellexec nowait

[UninstallRun]
Filename: "{app}\SmokeTrail.exe"; Parameters: "uninstall"; \
  RunOnceId: "RemoveService"; Flags: runhidden waituntilterminated

[Code]
var
  PortPage: TInputQueryWizardPage;

procedure InitializeWizard;
begin
  PortPage := CreateInputQueryPage(wpSelectDir,
    'Console port',
    'Which port should the web console listen on?',
    'SmokeTrail has no window of its own — you manage it in a browser. The installer' + #13#10 +
    'will open this port in Windows Firewall.');
  PortPage.Add('Port:', False);
  PortPage.Values[0] := '8518';
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  P: Integer;
begin
  Result := True;
  if (PortPage <> nil) and (CurPageID = PortPage.ID) then
  begin
    P := StrToIntDef(PortPage.Values[0], -1);
    if (P < 1) or (P > 65535) then
    begin
      MsgBox('Enter a port between 1 and 65535.', mbError, MB_OK);
      Result := False;
    end;
  end;
end;

function GetPort(Param: String): String;
begin
  if PortPage <> nil then
    Result := PortPage.Values[0]
  else
    Result := '8518';
end;

// The data directory is left behind on uninstall on purpose: removing the program
// is not a request to discard months of measurements. Ask, rather than decide.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir: String;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    DataDir := ExpandConstant('{commonappdata}\SmokeTrail');
    if DirExists(DataDir) then
      if MsgBox('Delete the collected history in' + #13#10 + DataDir + '?' + #13#10 + #13#10 +
                'Choose No to keep it for a future install.',
                mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
        DelTree(DataDir, True, True, True);
  end;
end;
