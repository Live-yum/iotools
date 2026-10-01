#ifndef IOTOOLS_NATIVE_H
#define IOTOOLS_NATIVE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ABI v1: UTF-8 byte lengths, not character counts or NUL-terminated strings.
 * All input buffers are borrowed for the duration of the call, never modified.
 * Nonempty inputs and both reply output pointers must be non-NULL.
 * Every non-NULL reply must be released exactly once with IotoolsNativeFree,
 * including error replies. Never free a reply with the host allocator.
 * Output bytes are NOT NUL-terminated. Copy exactly reply_n bytes before free.
 * Null/invalid output pointers cause failure without opening a session.
 */
enum {
    IOTOOLS_NATIVE_ABI_VERSION = 1,
    IOTOOLS_NATIVE_OK = 0,
    IOTOOLS_NATIVE_INVALID_ARGUMENT = 1,
    IOTOOLS_NATIVE_INVALID_SESSION = 2,
    IOTOOLS_NATIVE_RESOURCE_LIMIT = 3,
    IOTOOLS_NATIVE_READ_ONLY = 1,
    IOTOOLS_NATIVE_HISTORY = 2,
    IOTOOLS_NATIVE_REQUIRE_EXISTING = 4,
    IOTOOLS_NATIVE_PAUSE = 0,
    IOTOOLS_NATIVE_RESUME = 1,
    IOTOOLS_NATIVE_CLOSE = 2,
    IOTOOLS_NATIVE_MAX_PATH = 16384,
    IOTOOLS_NATIVE_MAX_COMMAND = 8 * 1024 * 1024,
    IOTOOLS_NATIVE_MAX_REPLY = 16 * 1024 * 1024,
    IOTOOLS_NATIVE_MAX_SESSIONS = 16
};

uint32_t IotoolsNativeABIVersion(void);

/* Returns a nonzero opaque session ID and a mobileapi state reply on success,
 * or zero and an error reply on failure. Unknown flag bits are rejected.
 * private_root must be an existing absolute app-private directory chosen by
 * the trusted platform host, never by JSON. path must be absolute and inside
 * that root. Recovery opens use REQUIRE_EXISTING; first-launch opens may create
 * the bundled local sample. Open never performs protocol/network operations.
 * At most 16 concurrent open/opening sessions; IDs are never reused.
 */
uint64_t IotoolsNativeOpen(uint8_t *path, size_t path_n,
                         uint8_t *private_root, size_t root_n, uint32_t flags,
                         uint8_t **reply, size_t *reply_n);

/* OK means the JSON reached the engine; inspect JSON ok/error separately.
 * Commands on one session may serialize in mobileapi. Lifecycle calls and
 * cancel/events commands can run concurrently, including from another isolate.
 * A command already in flight during Close can complete or report closed.
 */
int32_t IotoolsNativeCommand(uint64_t session, uint8_t *json, size_t json_n,
                            uint8_t **reply, size_t *reply_n);

/* Close invalidates the ID before cancellation; repeat Close returns
 * INVALID_SESSION. Resume never replays prior network operations.
 */
int32_t IotoolsNativeLifecycle(uint64_t session, int32_t action);
void IotoolsNativeFree(uint8_t *reply);

#ifdef __cplusplus
}
#endif
#endif
