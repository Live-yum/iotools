param([Parameter(Mandatory=$true)][string]$Archive)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes, System.Drawing, System.Windows.Forms
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
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$evidence = Join-Path $repo 'platform-evidence/windows-release'
New-Item -ItemType Directory -Force $evidence | Out-Null
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('iotools-release-' + [guid]::NewGuid())
New-Item -ItemType Directory $temporary | Out-Null
$application = $null; $receiver = $null; $window = $null
$settingsPath = $null; $settingsBytes = $null; $settingsExisted = $false; $settingsTouched = $false; $fixturePath = $null

function Get-Elements {
  if ($null -eq $script:window) { return @() }
  return @($script:window.FindAll([Windows.Automation.TreeScope]::Descendants,
    [Windows.Automation.Condition]::TrueCondition))
}
function Save-Tree([string]$Name) {
  $rows = foreach ($element in (Get-Elements)) {
    try {
      $item = $element.Current
      $value = ''; $pattern = $null
      if ($element.TryGetCurrentPattern([Windows.Automation.ValuePattern]::Pattern, [ref]$pattern)) {
        $value = $pattern.Current.Value
      }
      [ordered]@{ name=$item.Name; value=$value; type=$item.ControlType.ProgrammaticName;
        offscreen=$item.IsOffscreen; bounds=$item.BoundingRectangle.ToString() }
    } catch [Windows.Automation.ElementNotAvailableException] { }
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
  do {
    if ($script:application.HasExited) { throw "Release app exited with $($script:application.ExitCode)" }
    $matches = @(foreach ($element in (Get-Elements)) {
      try {
        $item = $element.Current
        $nameMatches = if ($Contains) { $item.Name.Contains($Text) } else { $item.Name -eq $Text }
        if ($nameMatches -and !$item.IsOffscreen -and $item.BoundingRectangle.Width -gt 0 -and $item.BoundingRectangle.Height -gt 0) {
          [pscustomobject]@{ element=$element; area=($item.BoundingRectangle.Width*$item.BoundingRectangle.Height) }
        }
      } catch [Windows.Automation.ElementNotAvailableException] { }
    })
    if ($matches.Count -gt 0) { return ($matches | Sort-Object area | Select-Object -First 1).element }
    Start-Sleep -Milliseconds 100
  } while ([DateTime]::UtcNow -lt $deadline)
  Save-Screen 'missing-control'; Save-Tree 'missing-control' | Out-Null
  throw "No visible Release UI control: $Text"
}
function Click-Visible([Windows.Automation.AutomationElement]$Element) {
  $item = $Element.Current
  $r = $item.BoundingRectangle
  if ($item.IsOffscreen -or $r.Width -le 0 -or $r.Height -le 0) { throw 'Refusing to click an invisible control' }
  [ReleaseWindow]::SetForegroundWindow($script:application.MainWindowHandle) | Out-Null
  [ReleaseWindow]::SetCursorPos([int]($r.Left+$r.Width/2),[int]($r.Top+$r.Height/2)) | Out-Null
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
  $window = [Windows.Automation.AutomationElement]::FromHandle($application.MainWindowHandle)
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
    build='exact packaged Release binary';writes=1;cancelled_writes=0;java_environment='removed from child; restricted PATH';
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
