// +build go1.18

#include "go_asm.h"
#include "funcdata.h"
#include "textflag.h"

TEXT ·DisallowInternalReplacer(SB), NOSPLIT, $0
    NO_LOCAL_POINTERS
    // rsp+232, r10
    MOVQ R10, AX
    RET




