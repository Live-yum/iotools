param([string]$Archive, [switch]$ValidateHelpers)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Add-Type -AssemblyName Accessibility, System.Drawing, System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class ReleaseWindow {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool MoveWindow(IntPtr h,int x,int y,int w,int hgt,bool repaint);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h,out RECT r);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint flags,uint dx,uint dy,uint data,UIntPtr extra);
}
'@
# PowerShell 7 replaces its default references when ReferencedAssemblies is
# supplied. Keep its official .NET reference set as well as Windows MSAA.
$compilerReferences = @(Get-ChildItem -LiteralPath (Join-Path $PSHOME 'ref') -Filter '*.dll' |
  Select-Object -ExpandProperty FullName)
if (@($compilerReferences | Where-Object { [IO.Path]::GetFileName($_) -eq 'Accessibility.dll' }).Count -eq 0) {
  $compilerReferences += [Accessibility.IAccessible].Assembly.Location
}
Add-Type -ReferencedAssemblies $compilerReferences @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using Accessibility;
public sealed class ReleaseAccessibleNode {
  public string Name = "", Value = "";
  public int Role, State, X, Y, Width, Height;
  public bool Offscreen { get { return (State & 0x18000) != 0; } }
  public string Bounds { get { return X+","+Y+","+Width+","+Height; } }
}
public static class ReleaseAccessibility {
  [DllImport("user32.dll", CharSet=CharSet.Unicode)]
  static extern IntPtr FindWindowEx(IntPtr parent, IntPtr after, string cls, string title);
  [DllImport("oleacc.dll")]
  static extern int AccessibleObjectFromWindow(IntPtr hwnd,uint id,ref Guid iid,
    [MarshalAs(UnmanagedType.Interface)] out IAccessible accessible);
  [DllImport("oleacc.dll")]
  static extern int AccessibleChildren(IAccessible parent,int start,int count,
    [Out,MarshalAs(UnmanagedType.LPArray,ArraySubType=UnmanagedType.Struct,SizeParamIndex=2)] object[] children,
    out int obtained);
  static void Visit(IAccessible accessible, object child, List<ReleaseAccessibleNode> rows, int depth) {
    if (depth > 40 || rows.Count >= 5000) throw new InvalidOperationException("Unexpected MSAA tree size");
    try {
      var row = new ReleaseAccessibleNode();
      try { row.Name = accessible.get_accName(child) ?? ""; } catch (COMException) { }
      try { row.Value = accessible.get_accValue(child) ?? ""; } catch (COMException) { }
      try { row.Role = Convert.ToInt32(accessible.get_accRole(child)); } catch (COMException) { }
      try { row.State = Convert.ToInt32(accessible.get_accState(child)); } catch (COMException) { }
      try { accessible.accLocation(out row.X,out row.Y,out row.Width,out row.Height,child); } catch (COMException) { }
      rows.Add(row);
      if (!(child is int) || (int)child != 0) return;
      int count = accessible.accChildCount;
      if (count == 0) return;
      if (count > 5000) throw new InvalidOperationException("Unexpected MSAA child count");
      var children = new object[count]; int obtained;
      int result = AccessibleChildren(accessible,0,count,children,out obtained);
      if (result < 0) Marshal.ThrowExceptionForHR(result);
      for (int i=0;i<obtained;i++) {
        var nested = children[i] as IAccessible;
        if (nested != null) Visit(nested,0,rows,depth+1);
        else if (children[i] is int) Visit(accessible,children[i],rows,depth+1);
      }
    } catch (COMException) { /* A frame can replace a node while it is read. */ }
  }
  public static ReleaseAccessibleNode[] Read(IntPtr topWindow) {
    var rows = new List<ReleaseAccessibleNode>();
    IntPtr flutter = FindWindowEx(topWindow,IntPtr.Zero,"FLUTTERVIEW",null);
    if (flutter == IntPtr.Zero) return rows.ToArray();
    Guid iid = new Guid("618736E0-3C3D-11CF-810C-00AA00389B71");
    IAccessible root;
    // Flutter 3.35 deliberately defaults to MSAA, not native UIA. This normal
    // assistive-technology query also asks its engine to publish semantics.
    int result = AccessibleObjectFromWindow(flutter,0xFFFFFFFC,ref iid,out root);
    if (result >= 0 && root != null) Visit(root,0,rows,0);
    return rows.ToArray();
  }
}
'@
if ($ValidateHelpers) { Write-Host 'PASS Windows MSAA and screenshot helper compilation'; exit 0 }
if (!$Archive) { throw 'An exact packaged Release ZIP is required' }
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$evidence = Join-Path $repo 'platform-evidence/windows-release'
New-Item -ItemType Directory -Force $evidence | Out-Null
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('iotools-release-' + [guid]::NewGuid())
New-Item -ItemType Directory $temporary | Out-Null
$application = $null; $receiver = $null; $window = $null
$settingsPath = $null; $settingsBytes = $null; $settingsExisted = $false; $settingsTouched = $false; $fixturePath = $null

function Get-Elements {
  if ($null -eq $script:application -or $script:application.HasExited) { return @() }
  return @([ReleaseAccessibility]::Read($script:application.MainWindowHandle))
}
function Save-Tree([string]$Name) {
  $rows = foreach ($element in (Get-Elements)) {
    [ordered]@{ name=$element.Name; value=$element.Value; role=$element.Role;
      state=$element.State; offscreen=$element.Offscreen; bounds=$element.Bounds }
  }
  $rows | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 (Join-Path $evidence "$Name-tree.json")
  return (($rows | ForEach-Object { $_.name; $_.value }) -join "`n")
}
function Save-Screen([string]$Name) {
  if ($null -eq $script:application -or $script:application.HasExited) { return }
  $r = New-Object ReleaseWindow+RECT
  if (![ReleaseWindow]::GetWindowRect($script:application.MainWindowHandle, [ref]$r)) { throw 'Window rectangle unavailable' }
  $bitmap = [Drawing.Bitmap]::new(($r.Right-$r.Left), ($r.Bottom-$r.Top))
  $graphics = [Drawing.Graphics]::FromImage($bitmap)
  try {
    $graphics.CopyFromScreen($r.Left,$r.Top,0,0,$bitmap.Size)
    $bitmap.Save((Join-Path $evidence "$Name.png"), [Drawing.Imaging.ImageFormat]::Png)
  } finally { $graphics.Dispose(); $bitmap.Dispose() }
}
function Find-Visible([string]$Text, [switch]$Contains, [int]$Seconds=30) {
  $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
  $lastBounds = $null
  do {
    if ($script:application.HasExited) { throw "Release app exited with $($script:application.ExitCode)" }
    $matches = @(foreach ($element in (Get-Elements)) {
      $nameMatches = if ($Contains) { $element.Name.Contains($Text) } else { $element.Name -eq $Text }
      if ($nameMatches -and !$element.Offscreen -and ($element.State -band 1) -eq 0 -and $element.Width -gt 0 -and $element.Height -gt 0) {
        [pscustomobject]@{ element=$element; area=($element.Width*$element.Height) }
      }
    })
    if ($matches.Count -gt 0) {
      $candidate = ($matches | Sort-Object area | Select-Object -First 1).element
      if ($candidate.Bounds -eq $lastBounds) { return $candidate }
      $lastBounds = $candidate.Bounds
    } else { $lastBounds = $null }
    Start-Sleep -Milliseconds 100
  } while ([DateTime]::UtcNow -lt $deadline)
  Save-Screen 'missing-control'; Save-Tree 'missing-control' | Out-Null
  throw "No visible Release UI control: $Text"
}
function Click-Visible($Element) {
  if ($Element.Offscreen -or ($Element.State -band 1) -ne 0 -or $Element.Width -le 0 -or $Element.Height -le 0) { throw 'Refusing to click an invisible or unavailable control' }
  [ReleaseWindow]::SetForegroundWindow($script:application.MainWindowHandle) | Out-Null
  [ReleaseWindow]::SetCursorPos([int]($Element.X+$Element.Width/2),[int]($Element.Y+$Element.Height/2)) | Out-Null
  [ReleaseWindow]::mouse_event(2,0,0,0,[UIntPtr]::Zero)
  [ReleaseWindow]::mouse_event(4,0,0,0,[UIntPtr]::Zero)
  Start-Sleep -Milliseconds 150
}
function Get-Received { return @(Get-Content -Raw -Encoding utf8 (Join-Path $temporary 'received.json') | ConvertFrom-Json) }

try {
  $Archive = (Resolve-Path $Archive).Path
  $archiveHash = (Get-FileHash $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
  Expand-Archive -LiteralPath $Archive -DestinationPath (Join-Path $temporary 'package')
  $exe = Join-Path $temporary 'package/Release/iotools.exe'
  if (!(Test-Path $exe)) { throw 'The exact packaged Release executable is absent' }
  $exeHash = (Get-FileHash $exe -Algorithm SHA256).Hash.ToLowerInvariant()
  $python = (Get-Command python -CommandType Application).Source
  $receiverInfo = [Diagnostics.ProcessStartInfo]::new($python)
  $receiverInfo.ArgumentList.Add((Join-Path $PSScriptRoot 'release-http-fixture.py'))
  $receiverInfo.ArgumentList.Add($temporary)
  $receiverInfo.UseShellExecute = $false
  $receiver = [Diagnostics.Process]::Start($receiverInfo)
  $deadline = [DateTime]::UtcNow.AddSeconds(15)
  while (!(Test-Path (Join-Path $temporary 'ready.json'))) {
    if ($receiver.HasExited -or [DateTime]::UtcNow -gt $deadline) { throw 'Owned loopback fixture did not start' }
    Start-Sleep -Milliseconds 100
  }
  $port = (Get-Content -Raw (Join-Path $temporary 'ready.json') | ConvertFrom-Json).port
  # Match path_provider_windows: known RoamingAppData + executable CompanyName /
  # ProductName + NativePlatformServices' final iotools directory. No registry or
  # known-folder changes are made. Preserve any pre-existing CI-test settings.
  $version = [Diagnostics.FileVersionInfo]::GetVersionInfo($exe)
  if ($version.CompanyName -ne 'io.github.liveyum' -or $version.ProductName -ne 'iotools') { throw 'Unexpected application identity' }
  $privateRoot = Join-Path ([Environment]::GetFolderPath('ApplicationData')) 'io.github.liveyum/iotools/iotools'
  New-Item -ItemType Directory -Force $privateRoot | Out-Null
  $settingsPath = Join-Path $privateRoot '.iotools-settings.json'
  $settingsExisted = Test-Path $settingsPath
  if ($settingsExisted) { $settingsBytes = [IO.File]::ReadAllBytes($settingsPath) }
  $fixtureName = 'release-smoke-' + [guid]::NewGuid() + '.yaml'
  $fixturePath = Join-Path $privateRoot $fixtureName
  @"
version: 1
requests:
  - id: release-exact
    name: Windows Release精确写入
    protocol: http
    action: POST
    endpoint: http://127.0.0.1:$port/echo
    timeout: 5s
    params:
      json:
        number: 18446744073709551615
        string: "18446744073709551615"
        text: "配置中文😀"
"@ | Set-Content -Encoding utf8NoBOM $fixturePath
  $settingsTouched = $true
  @{theme='dark';readOnly=$false;history=$false;collection=$fixtureName} | ConvertTo-Json -Compress |
    Set-Content -Encoding utf8NoBOM $settingsPath
  $start = [Diagnostics.ProcessStartInfo]::new($exe)
  $start.WorkingDirectory = Split-Path $exe
  $start.UseShellExecute = $false
  foreach ($key in @($start.Environment.Keys)) {
    if ($key -match '^(JAVA_HOME.*|JDK_HOME|JRE_HOME|CLASSPATH)$') { $start.Environment.Remove($key) | Out-Null }
  }
  $start.Environment['PATH'] = "$($start.WorkingDirectory);$env:WINDIR\System32;$env:WINDIR"
  $application = [Diagnostics.Process]::Start($start)
  $deadline = [DateTime]::UtcNow.AddSeconds(30)
  do {
    if ($application.HasExited) { throw "Packaged Release exited: $($application.ExitCode)" }
    $application.Refresh()
    if ($application.MainWindowHandle -ne [IntPtr]::Zero) { break }
    if ([DateTime]::UtcNow -gt $deadline) { throw 'Release app did not create a window' }
    Start-Sleep -Milliseconds 100
  } while ($true)
  $screen = [Windows.Forms.Screen]::PrimaryScreen.WorkingArea
  [ReleaseWindow]::MoveWindow($application.MainWindowHandle,$screen.Left,$screen.Top,
    [Math]::Min(1280,$screen.Width),[Math]::Min(900,$screen.Height),$true) | Out-Null
  [ReleaseWindow]::SetForegroundWindow($application.MainWindowHandle) | Out-Null
  Get-Elements | Out-Null
  $card = Find-Visible 'Windows Release精确写入' -Contains
  Save-Screen '01-installed-release-home'; Save-Tree '01-installed-release-home' | Out-Null
  Click-Visible $card
  Click-Visible (Find-Visible '执行')
  Find-Visible '确认执行写操作' | Out-Null
  $review = Save-Tree '02-exact-review'
  if (!$review.Contains('"number":18446744073709551615') -or !$review.Contains('"string":"18446744073709551615"')) { throw 'Release review lost exact number/string distinction' }
  Save-Screen '02-exact-review'
  if (@(Get-Received).Count -ne 0) { throw 'Preview unexpectedly sent HTTP' }
  Click-Visible (Find-Visible '取消')
  if (@(Get-Received).Count -ne 0) { throw 'Cancel unexpectedly sent HTTP' }
  Click-Visible (Find-Visible '执行')
  Click-Visible (Find-Visible '确认执行')
  Find-Visible '已完成' | Out-Null
  Find-Visible 'Windows Release真实响应😀' -Contains | Out-Null
  $bodies = @(Get-Received)
  if ($bodies.Count -ne 1 -or !$bodies[0].Contains('"number":18446744073709551615') -or !$bodies[0].Contains('"string":"18446744073709551615"') -or !$bodies[0].Contains('配置中文😀')) { throw 'Wrong exact Release HTTP payload/count' }
  Save-Screen '03-real-http-response'; Save-Tree '03-real-http-response' | Out-Null
  $application.Refresh()
  $modules = @($application.Modules | ForEach-Object { [ordered]@{name=$_.ModuleName;path=$_.FileName} })
  $modules | ConvertTo-Json -Depth 3 | Set-Content -Encoding utf8 (Join-Path $evidence 'loaded-modules.json')
  if (@($modules | Where-Object { $_.name -match '^(jvm|java|dartjni)\.dll$' }).Count -ne 0) { throw 'Unexpected JVM/JNI runtime module loaded' }
  if ((Get-FileHash $exe -Algorithm SHA256).Hash.ToLowerInvariant() -ne $exeHash) { throw 'Release executable changed during test' }
  [ordered]@{status='passed';source_sha=$env:IOTOOLS_SHA;archive_sha256=$archiveHash;exe_sha256=$exeHash;
    build='exact packaged Release binary';writes=1;cancelled_writes=0;accessibility='Windows MSAA/IAccessible';java_environment='removed from child; restricted PATH';
    jvm_modules_loaded=$false;os=[Environment]::OSVersion.VersionString;scope='normal native GUI actions to actual loopback HTTP; no application-internal hooks'} |
    ConvertTo-Json -Depth 4 | Set-Content -Encoding utf8 (Join-Path $evidence 'result.json')
  Write-Host 'PASS exact packaged Windows Release GUI + Go HTTP with sanitized Java environment and no loaded JVM/JNI'
} catch {
  try { Save-Screen 'failure'; Save-Tree 'failure' | Out-Null } catch { }
  throw
} finally {
  if ($null -ne $application -and !$application.HasExited) {
    $application.CloseMainWindow() | Out-Null
    if (!$application.WaitForExit(5000)) { $application.Kill(); $application.WaitForExit() }
  }
  if ($null -ne $receiver -and !$receiver.HasExited) { $receiver.Kill(); $receiver.WaitForExit() }
  if ($null -ne $fixturePath -and (Test-Path $fixturePath)) { Remove-Item -LiteralPath $fixturePath }
  if ($settingsTouched) {
    if ($settingsExisted) { [IO.File]::WriteAllBytes($settingsPath, $settingsBytes) }
    elseif (Test-Path $settingsPath) { Remove-Item -LiteralPath $settingsPath }
  }
  # Retain extracted bytes in the runner temp directory for post-failure analysis.
}
