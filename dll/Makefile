# NMake Makefile for smtc.dll
# Usage: nmake /f Makefile
# Requires: VS Developer Command Prompt (vcvarsall.bat amd64)

# Windows SDK C++/WinRT headers
WINSDK_CPPWINRT = "C:\Program Files (x86)\Windows Kits\10\Include\10.0.26100.0\cppwinrt"

CXX      = cl
CXXFLAGS = /std:c++17 /O2 /EHsc /MT /DWIN32_LEAN_AND_MEAN /DNOMINMAX /DSMTC_EXPORTS /I$(WINSDK_CPPWINRT)
LIBS     = windowsapp.lib ole32.lib
TARGET   = smtc.dll

all: $(TARGET)

smtc.obj: smtc.cpp smtc.h
	$(CXX) $(CXXFLAGS) /c smtc.cpp /Fo:smtc.obj

$(TARGET): smtc.obj
	$(CXX) /LD smtc.obj $(LIBS) /Fe:$(TARGET)

clean:
	del *.obj *.dll *.lib *.exp *.pdb 2>nul
