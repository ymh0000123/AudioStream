param([long]$PositionMs = 0)

$ErrorActionPreference = 'SilentlyContinue'
try {
    [Windows.Media.Control.SystemMediaTransportControlsSessionManager,Windows.Media.Control,ContentType=WindowsRuntime] | Out-Null
    $task = [Windows.Media.Control.SystemMediaTransportControlsSessionManager]::RequestAsync()
    $mgr = $task.GetAwaiter().GetResult()
    $sess = $mgr.GetCurrentSession()
    if ($null -eq $sess) {
        exit 1
    }
    $pos100ns = $PositionMs * 10000
    $result = $sess.TryChangePlaybackPositionAsync($pos100ns).GetAwaiter().GetResult()
    if ($result) {
        exit 0
    } else {
        exit 2
    }
} catch {
    exit 3
}
