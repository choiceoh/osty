#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Active LLVM/native GC runtime path. See ../../../../RUNTIME_GC.md. */

typedef void (*osty_gc_trace_fn)(void *payload);
typedef void (*osty_gc_destroy_fn)(void *payload);

typedef struct osty_gc_header {
    struct osty_gc_header *next;
    struct osty_gc_header *prev;
    int64_t object_kind;
    int64_t byte_size;
    int64_t root_count;
    bool marked;
    osty_gc_trace_fn trace;
    osty_gc_destroy_fn destroy;
    const char *site;
    void *payload;
} osty_gc_header;

typedef struct osty_rt_list {
    int64_t len;
    int64_t cap;
    size_t elem_size;
    bool pointer_elems;
    unsigned char *data;
} osty_rt_list;

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

enum {
    OSTY_GC_KIND_GENERIC = 1,
    OSTY_GC_KIND_LIST = 1024,
    OSTY_GC_KIND_STRING = 1025,
};

static osty_gc_header *osty_gc_objects = NULL;
static int64_t osty_gc_live_count = 0;
static int64_t osty_gc_live_bytes = 0;
static int64_t osty_gc_collection_count = 0;
static int64_t osty_gc_allocated_since_collect = 0;
static bool osty_gc_safepoint_stress_loaded = false;
static bool osty_gc_safepoint_stress_enabled = false;
static bool osty_gc_pressure_limit_loaded = false;
static int64_t osty_gc_pressure_limit_bytes = 32768;
static bool osty_gc_collection_requested = false;
static int64_t osty_gc_pre_write_count = 0;
static int64_t osty_gc_pre_write_managed_count = 0;
static int64_t osty_gc_post_write_count = 0;
static int64_t osty_gc_post_write_managed_count = 0;
static int64_t osty_gc_load_count = 0;
static int64_t osty_gc_load_managed_count = 0;

void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

static void osty_rt_abort(const char *message) {
    fprintf(stderr, "osty llvm runtime: %s\n", message);
    abort();
}

static void osty_gc_link(osty_gc_header *header) {
    header->next = osty_gc_objects;
    header->prev = NULL;
    if (osty_gc_objects != NULL) {
        osty_gc_objects->prev = header;
    }
    osty_gc_objects = header;
    osty_gc_live_count += 1;
    osty_gc_live_bytes += header->byte_size;
}

static void osty_gc_unlink(osty_gc_header *header) {
    if (header->prev != NULL) {
        header->prev->next = header->next;
    } else {
        osty_gc_objects = header->next;
    }
    if (header->next != NULL) {
        header->next->prev = header->prev;
    }
    osty_gc_live_count -= 1;
    osty_gc_live_bytes -= header->byte_size;
}

static int64_t osty_gc_pressure_limit_now(void) {
    const char *value;
    char *end = NULL;
    long long parsed;

    if (osty_gc_pressure_limit_loaded) {
        return osty_gc_pressure_limit_bytes;
    }
    osty_gc_pressure_limit_loaded = true;
    value = getenv("OSTY_GC_THRESHOLD_BYTES");
    if (value == NULL || value[0] == '\0') {
        return osty_gc_pressure_limit_bytes;
    }
    parsed = strtoll(value, &end, 10);
    if (end == value || (end != NULL && *end != '\0') || parsed < 0) {
        osty_rt_abort("invalid OSTY_GC_THRESHOLD_BYTES");
    }
    osty_gc_pressure_limit_bytes = (int64_t)parsed;
    return osty_gc_pressure_limit_bytes;
}

static void osty_gc_note_allocation(size_t payload_size) {
    int64_t pressure_limit = osty_gc_pressure_limit_now();

    if (payload_size > (size_t)INT64_MAX) {
        osty_rt_abort("GC payload size overflow");
    }
    osty_gc_allocated_since_collect += (int64_t)payload_size;
    if (pressure_limit <= 0) {
        return;
    }
    if (osty_gc_live_bytes >= pressure_limit || osty_gc_allocated_since_collect >= pressure_limit) {
        osty_gc_collection_requested = true;
    }
}

static osty_gc_header *osty_gc_find_header(void *payload) {
    osty_gc_header *header = osty_gc_objects;
    while (header != NULL) {
        if (header->payload == payload) {
            return header;
        }
        header = header->next;
    }
    return NULL;
}

static void *osty_gc_allocate_managed(size_t byte_size, int64_t object_kind, const char *site, osty_gc_trace_fn trace, osty_gc_destroy_fn destroy) {
    osty_gc_header *header;
    size_t payload_size = byte_size;
    size_t total_size;

    if (payload_size == 0) {
        payload_size = 1;
    }
    if (payload_size > SIZE_MAX - sizeof(osty_gc_header)) {
        osty_rt_abort("GC allocation overflow");
    }
    total_size = sizeof(osty_gc_header) + payload_size;
    header = (osty_gc_header *)calloc(1, total_size);
    if (header == NULL) {
        osty_rt_abort("out of memory");
    }
    header->object_kind = object_kind;
    header->byte_size = (int64_t)payload_size;
    header->trace = trace;
    header->destroy = destroy;
    header->site = site;
    header->payload = (void *)(header + 1);
    osty_gc_link(header);
    osty_gc_note_allocation(payload_size);
    return header->payload;
}

static void osty_gc_mark_payload(void *payload);

static void osty_rt_list_trace(void *payload) {
    osty_rt_list *list = (osty_rt_list *)payload;
    int64_t i;

    if (list == NULL || !list->pointer_elems || list->data == NULL) {
        return;
    }
    for (i = 0; i < list->len; i++) {
        void *child = NULL;
        memcpy(&child, list->data + ((size_t)i * list->elem_size), sizeof(child));
        if (child != NULL) {
            osty_gc_mark_payload(child);
        }
    }
}

static void osty_rt_list_destroy(void *payload) {
    osty_rt_list *list = (osty_rt_list *)payload;
    if (list != NULL) {
        free(list->data);
    }
}

static void osty_gc_mark_header(osty_gc_header *header) {
    if (header == NULL || header->marked) {
        return;
    }
    header->marked = true;
    if (header->trace != NULL) {
        header->trace(header->payload);
    }
}

static void osty_gc_mark_payload(void *payload) {
    osty_gc_mark_header(osty_gc_find_header(payload));
}

static void osty_gc_mark_root_slot(void *slot_addr) {
    void *payload = NULL;

    if (slot_addr == NULL) {
        return;
    }
    memcpy(&payload, slot_addr, sizeof(payload));
    if (payload == NULL) {
        return;
    }
    osty_gc_mark_payload(payload);
}

static void osty_gc_collect_now_with_stack_roots(void *const *root_slots, int64_t root_slot_count) {
    osty_gc_header *header = osty_gc_objects;
    osty_gc_header *next;
    int64_t i;

    while (header != NULL) {
        header->marked = false;
        header = header->next;
    }
    header = osty_gc_objects;
    while (header != NULL) {
        if (header->root_count > 0) {
            osty_gc_mark_header(header);
        }
        header = header->next;
    }
    for (i = 0; i < root_slot_count; i++) {
        osty_gc_mark_root_slot((void *)root_slots[i]);
    }
    header = osty_gc_objects;
    while (header != NULL) {
        next = header->next;
        if (!header->marked) {
            if (header->destroy != NULL) {
                header->destroy(header->payload);
            }
            osty_gc_unlink(header);
            free(header);
        }
        header = next;
    }
    osty_gc_collection_count += 1;
    osty_gc_allocated_since_collect = 0;
    osty_gc_collection_requested = false;
}

static void osty_gc_collect_now(void) {
    osty_gc_collect_now_with_stack_roots(NULL, 0);
}

static bool osty_gc_safepoint_stress_enabled_now(void) {
    const char *value;

    if (osty_gc_safepoint_stress_loaded) {
        return osty_gc_safepoint_stress_enabled;
    }
    osty_gc_safepoint_stress_loaded = true;
    value = getenv("OSTY_GC_STRESS");
    if (value == NULL || value[0] == '\0' || strcmp(value, "0") == 0 || strcmp(value, "false") == 0 || strcmp(value, "FALSE") == 0) {
        osty_gc_safepoint_stress_enabled = false;
    } else {
        osty_gc_safepoint_stress_enabled = true;
    }
    return osty_gc_safepoint_stress_enabled;
}

static osty_rt_list *osty_rt_list_cast(void *raw_list) {
    if (raw_list == NULL) {
        osty_rt_abort("list is null");
    }
    return (osty_rt_list *)raw_list;
}

static void osty_rt_list_ensure_layout(osty_rt_list *list, size_t elem_size, bool pointer_elems) {
    if (list->elem_size == 0) {
        list->elem_size = elem_size;
        list->pointer_elems = pointer_elems;
        return;
    }
    if (list->elem_size != elem_size) {
        osty_rt_abort("list element size mismatch");
    }
    if (list->pointer_elems != pointer_elems) {
        osty_rt_abort("list element pointer-kind mismatch");
    }
}

static void osty_rt_list_reserve(osty_rt_list *list, int64_t min_cap) {
    int64_t next_cap = list->cap;
    void *next_data;
    size_t want_bytes;

    if (min_cap <= list->cap) {
        return;
    }
    if (list->elem_size == 0) {
        osty_rt_abort("list element size is zero");
    }
    if (next_cap < 4) {
        next_cap = 4;
    }
    while (next_cap < min_cap) {
        if (next_cap > INT64_MAX / 2) {
            next_cap = min_cap;
            break;
        }
        next_cap *= 2;
    }
    want_bytes = (size_t)next_cap * list->elem_size;
    if (list->elem_size != 0 && want_bytes / list->elem_size != (size_t)next_cap) {
        osty_rt_abort("list allocation overflow");
    }
    next_data = realloc(list->data, want_bytes);
    if (next_data == NULL) {
        osty_rt_abort("out of memory");
    }
    list->data = (unsigned char *)next_data;
    list->cap = next_cap;
}

static void osty_rt_list_push_bytes(void *raw_list, const void *value, size_t elem_size, bool pointer_elems) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_layout(list, elem_size, pointer_elems);
    osty_rt_list_reserve(list, list->len + 1);
    memcpy(list->data + ((size_t)list->len * list->elem_size), value, elem_size);
    list->len += 1;
}

static void *osty_rt_list_get_bytes(void *raw_list, int64_t index, size_t elem_size, bool pointer_elems) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (index < 0 || index >= list->len) {
        osty_rt_abort("list index out of range");
    }
    osty_rt_list_ensure_layout(list, elem_size, pointer_elems);
    return list->data + ((size_t)index * list->elem_size);
}

static char *osty_rt_string_dup_range(const char *start, size_t len) {
    char *out = (char *)osty_gc_allocate_managed(len + 1, OSTY_GC_KIND_STRING, "runtime.strings.split.part", NULL, NULL);
    if (len != 0) {
        memcpy(out, start, len);
    }
    out[len] = '\0';
    return out;
}

void *osty_rt_list_new(void) {
    return osty_gc_allocate_managed(sizeof(osty_rt_list), OSTY_GC_KIND_LIST, "runtime.list", osty_rt_list_trace, osty_rt_list_destroy);
}

int64_t osty_rt_list_len(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    return list->len;
}

void osty_rt_list_push_i64(void *raw_list, int64_t value) {
    osty_rt_list_push_bytes(raw_list, &value, sizeof(value), false);
}

void osty_rt_list_push_i1(void *raw_list, bool value) {
    osty_rt_list_push_bytes(raw_list, &value, sizeof(value), false);
}

void osty_rt_list_push_f64(void *raw_list, double value) {
    osty_rt_list_push_bytes(raw_list, &value, sizeof(value), false);
}

void osty_rt_list_push_ptr(void *raw_list, void *value) {
    osty_rt_list_push_bytes(raw_list, &value, sizeof(value), true);
    osty_gc_post_write_v1(raw_list, value, OSTY_GC_KIND_LIST);
}

int64_t osty_rt_list_get_i64(void *raw_list, int64_t index) {
    int64_t value;
    memcpy(&value, osty_rt_list_get_bytes(raw_list, index, sizeof(value), false), sizeof(value));
    return value;
}

bool osty_rt_list_get_i1(void *raw_list, int64_t index) {
    bool value;
    memcpy(&value, osty_rt_list_get_bytes(raw_list, index, sizeof(value), false), sizeof(value));
    return value;
}

double osty_rt_list_get_f64(void *raw_list, int64_t index) {
    double value;
    memcpy(&value, osty_rt_list_get_bytes(raw_list, index, sizeof(value), false), sizeof(value));
    return value;
}

void *osty_rt_list_get_ptr(void *raw_list, int64_t index) {
    void *value;
    memcpy(&value, osty_rt_list_get_bytes(raw_list, index, sizeof(value), true), sizeof(value));
    return osty_gc_load_v1(value);
}

bool osty_rt_strings_Equal(const char *left, const char *right) {
    if (left == NULL || right == NULL) {
        return left == right;
    }
    return strcmp(left, right) == 0;
}

bool osty_rt_strings_HasPrefix(const char *value, const char *prefix) {
    size_t prefix_len;
    if (value == NULL || prefix == NULL) {
        return false;
    }
    prefix_len = strlen(prefix);
    return strncmp(value, prefix, prefix_len) == 0;
}

void *osty_rt_strings_Split(const char *value, const char *sep) {
    osty_rt_list *out = (osty_rt_list *)osty_rt_list_new();
    const char *cursor;
    const char *next;
    size_t sep_len;

    if (value == NULL) {
        return out;
    }
    if (sep == NULL || sep[0] == '\0') {
        while (*value != '\0') {
            char *piece = osty_rt_string_dup_range(value, 1);
            osty_rt_list_push_ptr(out, piece);
            value += 1;
        }
        return out;
    }
    sep_len = strlen(sep);
    cursor = value;
    while ((next = strstr(cursor, sep)) != NULL) {
        char *piece = osty_rt_string_dup_range(cursor, (size_t)(next - cursor));
        osty_rt_list_push_ptr(out, piece);
        cursor = next + sep_len;
    }
    osty_rt_list_push_ptr(out, osty_rt_string_dup_range(cursor, strlen(cursor)));
    return out;
}

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) {
    if (byte_size < 0) {
        osty_rt_abort("negative GC allocation size");
    }
    return osty_gc_allocate_managed((size_t)byte_size, object_kind == 0 ? OSTY_GC_KIND_GENERIC : object_kind, site, NULL, NULL);
}

void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) {
    osty_gc_header *owner_header;

    osty_gc_pre_write_count += 1;
    (void)slot_kind;
    if (old_value == NULL) {
        return;
    }
    if (osty_gc_find_header(old_value) == NULL) {
        return;
    }
    osty_gc_pre_write_managed_count += 1;
    owner_header = osty_gc_find_header(owner);
    if (owner_header != NULL && owner_header->root_count > 0) {
        osty_gc_collection_requested = true;
    }
}

void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) {
    osty_gc_header *owner_header;

    osty_gc_post_write_count += 1;
    (void)slot_kind;
    if (owner == NULL || value == NULL) {
        return;
    }
    owner_header = osty_gc_find_header(owner);
    if (owner_header == NULL) {
        return;
    }
    if (osty_gc_find_header(value) == NULL) {
        return;
    }
    osty_gc_post_write_managed_count += 1;
    if (owner_header->root_count > 0) {
        osty_gc_collection_requested = true;
    }
}

void *osty_gc_load_v1(void *value) {
    osty_gc_load_count += 1;
    if (osty_gc_find_header(value) != NULL) {
        osty_gc_load_managed_count += 1;
    }
    return value;
}

void osty_gc_root_bind_v1(void *root) {
    osty_gc_header *header = osty_gc_find_header(root);
    if (header == NULL) {
        return;
    }
    if (header->root_count == INT64_MAX) {
        osty_rt_abort("GC root count overflow");
    }
    header->root_count += 1;
}

void osty_gc_root_release_v1(void *root) {
    osty_gc_header *header = osty_gc_find_header(root);
    if (header == NULL) {
        return;
    }
    if (header->root_count <= 0) {
        osty_rt_abort("GC root release underflow");
    }
    header->root_count -= 1;
}

void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) {
    (void)safepoint_id;
    if (root_slot_count < 0) {
        osty_rt_abort("negative safepoint root slot count");
    }
    if (!osty_gc_safepoint_stress_enabled_now() && !osty_gc_collection_requested) {
        return;
    }
    osty_gc_collect_now_with_stack_roots(root_slots, root_slot_count);
}

void osty_gc_debug_collect(void) {
    osty_gc_collect_now();
}

int64_t osty_gc_debug_live_count(void) {
    return osty_gc_live_count;
}

int64_t osty_gc_debug_collection_count(void) {
    return osty_gc_collection_count;
}

int64_t osty_gc_debug_live_bytes(void) {
    return osty_gc_live_bytes;
}

int64_t osty_gc_debug_pre_write_count(void) {
    return osty_gc_pre_write_count;
}

int64_t osty_gc_debug_pre_write_managed_count(void) {
    return osty_gc_pre_write_managed_count;
}

int64_t osty_gc_debug_post_write_count(void) {
    return osty_gc_post_write_count;
}

int64_t osty_gc_debug_post_write_managed_count(void) {
    return osty_gc_post_write_managed_count;
}

int64_t osty_gc_debug_load_count(void) {
    return osty_gc_load_count;
}

int64_t osty_gc_debug_load_managed_count(void) {
    return osty_gc_load_managed_count;
}
