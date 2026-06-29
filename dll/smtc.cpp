#include "smtc.h"
#include <atomic>
#include <cstring>
#include <mutex>
#include <string>
#include <windows.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Media.Control.h>
#include <winrt/impl/Windows.Foundation.Collections.0.h>

#pragma comment(lib, "windowsapp")

using namespace winrt;
using namespace Windows::Foundation;
using namespace Windows::Foundation::Collections;
using namespace Windows::Media::Control;

using SMTCMgr = GlobalSystemMediaTransportControlsSessionManager;
using SMTCSession = GlobalSystemMediaTransportControlsSession;
using SMTCStatus = GlobalSystemMediaTransportControlsSessionPlaybackStatus;

static std::atomic<bool> g_ok{false};
static SMTCMgr g_mgr{nullptr};
static std::mutex g_mu;
static HANDLE g_hReady = NULL;

static char g_outBuf[4096];
static int32_t g_outLen = 0;

static int EscJson(const std::string& s, char* d, int ds) {
    int p = 0;
    for (char c : s) {
        if (c=='"'||c=='\\') { if(p<ds-2){d[p++]='\\';d[p++]=c;} }
        else if (c=='\n') { if(p<ds-2){d[p++]='\\';d[p++]='n';} }
        else if (c=='\r') { if(p<ds-2){d[p++]='\\';d[p++]='r';} }
        else if (c=='\t') { if(p<ds-2){d[p++]='\\';d[p++]='t';} }
        else if ((unsigned char)c<0x20) { p+=snprintf(d+p,ds-p,"\\u%04x",(unsigned char)c); }
        else { if(p<ds-1) d[p++]=c; }
    }
    if(p<ds) d[p]=0;
    return p;
}

static void DoQuery() {
    if (!g_mgr) { g_outBuf[0]=0; g_outLen=0; return; }
    auto ss = g_mgr.GetSessions();
    SMTCSession act{nullptr}, first{nullptr};
    for (auto const& s : ss) {
        if (!first) first = s;
        try { if (s.GetPlaybackInfo().PlaybackStatus()==SMTCStatus::Playing){act=s;break;} } catch(...){}
    }
    if (!act&&first) act = first;
    if (!act) { g_outBuf[0]=0; g_outLen=0; return; }
    bool playing=false; std::string t,a,al; int64_t pos=0,dur=0;
    try{playing=(act.GetPlaybackInfo().PlaybackStatus()==SMTCStatus::Playing);}catch(...){}
    try{auto mp=act.TryGetMediaPropertiesAsync().get();t=winrt::to_string(mp.Title());a=winrt::to_string(mp.Artist());al=winrt::to_string(mp.AlbumTitle());}catch(...){}
    try{auto tl=act.GetTimelineProperties();pos=tl.Position().count()/10000;dur=tl.EndTime().count()/10000;}catch(...){}
    char et[512],ea[512],eb[512];
    EscJson(t,et,sizeof(et)); EscJson(a,ea,sizeof(ea)); EscJson(al,eb,sizeof(eb));
    g_outLen = snprintf(g_outBuf, sizeof(g_outBuf),
        "{\"has_session\":true,\"playing\":%s,\"title\":\"%s\","
        "\"artist\":\"%s\",\"album\":\"%s\",\"position\":%lld,\"duration\":%lld}",
        playing?"true":"false",et,ea,eb,(long long)pos,(long long)dur);
}

// Init thread: MTA init, then use resume_background to get manager on threadpool
static DWORD WINAPI InitThread(LPVOID) {
    try {
        winrt::init_apartment(winrt::apartment_type::multi_threaded);
    } catch (...) { SetEvent(g_hReady); return 1; }

    try {
        // RequestAsync then get() on threadpool via resume_background
        auto op = SMTCMgr::RequestAsync();
        // The .get() call on MTA should work if we're on a threadpool thread
        // But we need to block this thread. Use a blocking wait with timeout.
        // Actually, on MTA, the completion fires on a threadpool thread.
        // .get() should unblock when that happens.
        // The issue was that .get() deadlocked — let's try a different approach:
        // poll Status() with Sleep in a loop
        for (int i = 0; i < 500; i++) { // 5 seconds max
            if (op.Status() != AsyncStatus::Started) break;
            Sleep(10);
        }
        if (op.Status() == AsyncStatus::Completed) {
            g_mgr = op.GetResults();
        }
    } catch (...) { g_mgr = nullptr; }

    g_ok = (g_mgr != nullptr);
    SetEvent(g_hReady);
    return 0;
}

extern "C" {

int32_t SMTC_API SmtcInit(void) {
    if (g_ok) return 0;
    g_hReady = CreateEventW(NULL, TRUE, FALSE, NULL);
    HANDLE hThread = CreateThread(NULL, 0, InitThread, NULL, 0, NULL);
    if (!hThread) return 1;
    WaitForSingleObject(g_hReady, 10000);
    CloseHandle(hThread);
    return g_ok ? 0 : 1;
}

int32_t SMTC_API SmtcQuery(char* buf, int32_t bs) {
    if (!g_ok || !buf || bs <= 0) return -1;
    std::lock_guard lk(g_mu);
    DoQuery();
    if (g_outLen <= 0) { buf[0]=0; return 0; }
    int32_t n = g_outLen;
    if (n >= bs) n = bs - 1;
    memcpy(buf, g_outBuf, n);
    buf[n] = '\0';
    return n;
}

void SMTC_API SmtcClose(void) {
    g_ok = false;
    g_mgr = nullptr;
    winrt::uninit_apartment();
}

}
