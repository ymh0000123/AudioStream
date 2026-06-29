#pragma once
#include <stdint.h>

#ifdef SMTC_EXPORTS
#define SMTC_API __declspec(dllexport)
#else
#define SMTC_API __declspec(dllimport)
#endif

#ifdef __cplusplus
extern "C" {
#endif

SMTC_API int32_t SmtcInit(void);
SMTC_API int32_t SmtcQuery(char* buf, int32_t buf_size);
SMTC_API void    SmtcClose(void);

#ifdef __cplusplus
}
#endif
