#include "iotools_native.h"
#include <stdio.h>
#include <string.h>

/* Compile this consumer against the checked-in header and the built library to
 * catch drift between the documented C types and the exported ABI. */
int main(int argc, char **argv) {
    if (argc != 3 || IotoolsNativeABIVersion() != IOTOOLS_NATIVE_ABI_VERSION) return 1;
    uint8_t *reply = NULL;
    size_t reply_n = 0;
    uint64_t session = IotoolsNativeOpen((uint8_t *)argv[1], strlen(argv[1]),
        (uint8_t *)argv[2], strlen(argv[2]), 0, &reply, &reply_n);
    if (!session || !reply || !reply_n || reply_n > IOTOOLS_NATIVE_MAX_REPLY) return 2;
    IotoolsNativeFree(reply);
    uint8_t command[] = "{\"op\":\"state\"}";
    if (IotoolsNativeCommand(session, command, sizeof(command) - 1, &reply, &reply_n) != IOTOOLS_NATIVE_OK) return 3;
    if (!reply || !reply_n || reply[reply_n - 1] != '}') return 4;
    IotoolsNativeFree(reply);
    if (IotoolsNativeLifecycle(session, IOTOOLS_NATIVE_PAUSE) != IOTOOLS_NATIVE_OK) return 5;
    if (IotoolsNativeLifecycle(session, IOTOOLS_NATIVE_RESUME) != IOTOOLS_NATIVE_OK) return 6;
    if (IotoolsNativeLifecycle(session, IOTOOLS_NATIVE_CLOSE) != IOTOOLS_NATIVE_OK) return 7;
    if (IotoolsNativeLifecycle(session, IOTOOLS_NATIVE_CLOSE) != IOTOOLS_NATIVE_INVALID_SESSION) return 8;
    IotoolsNativeFree(NULL);
    puts("PASS C header consumer");
    return 0;
}
