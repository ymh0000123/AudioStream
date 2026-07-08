# NMake Makefile for smtc.dll
# Usage: nmake /f Makefile
# Requires: VS Developer Command Prompt (vcvarsall.bat amd64)

# Windows SDK C++/WinRT headers
# /std:c++20 required: uses <coroutine> instead of <experimental/coroutine>;
# VS 2026's MSVC treats the experimental header as a hard error (C2338).
WINSDK_CPPWINRT = "C:\Program Files (x86)\Windows Kits\10\Include\10.0.26100.0\cppwinrt"

CXX      = cl
CXXFLAGS = /std:c++20 /O2 /EHsc /MT /DWIN32_LEAN_AND_MEAN /DNOMINMAX /DSMTC_EXPORTS /I$(WINSDK_CPPWINRT)
LIBS     = windowsapp.lib ole32.lib
TARGET   = smtc.dll

all: $(TARGET)

smtc.obj: smtc.cpp smtc.h
	$(CXX) $(CXXFLAGS) /c smtc.cpp /Fo:smtc.obj

$(TARGET): smtc.obj
	$(CXX) /LD smtc.obj $(LIBS) /Fe:$(TARGET)

clean:
	del *.obj *.dll *.lib *.exp *.pdb 2>nul
