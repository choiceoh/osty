#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct osty_rt_list {
    int64_t len;
    int64_t cap;
    size_t elem_size;
    unsigned char *data;
} osty_rt_list;

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

static void osty_rt_abort(const char *message) {
    fprintf(stderr, "osty llvm runtime: %s\n", message);
    abort();
}

static osty_rt_list *osty_rt_list_cast(void *raw_list) {
    if (raw_list == NULL) {
        osty_rt_abort("list is null");
    }
    return (osty_rt_list *)raw_list;
}

static void osty_rt_list_ensure_elem_size(osty_rt_list *list, size_t elem_size) {
    if (list->elem_size == 0) {
        list->elem_size = elem_size;
        return;
    }
    if (list->elem_size != elem_size) {
        osty_rt_abort("list element size mismatch");
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

static void osty_rt_list_push_bytes(void *raw_list, const void *value, size_t elem_size) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_elem_size(list, elem_size);
    osty_rt_list_reserve(list, list->len + 1);
    memcpy(list->data + ((size_t)list->len * list->elem_size), value, elem_size);
    list->len += 1;
}

static void *osty_rt_list_get_bytes(void *raw_list, int64_t index, size_t elem_size) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (index < 0 || index >= list->len) {
        osty_rt_abort("list index out of range");
    }
    osty_rt_list_ensure_elem_size(list, elem_size);
    return list->data + ((size_t)index * list->elem_size);
}

static char *osty_rt_string_dup_range(const char *start, size_t len) {
    char *out = (char *)malloc(len + 1);
    if (out == NULL) {
        osty_rt_abort("out of memory");
    }
    if (len != 0) {
        memcpy(out, start, len);
    }
    out[len] = '\0';
    return out;
}

void *osty_rt_list_new(void) {
    osty_rt_list *list = (osty_rt_list *)calloc(1, sizeof(osty_rt_list));
    if (list == NULL) {
        osty_rt_abort("out of memory");
    }
    return list;
}

int64_t osty_rt_list_len(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    return list->len;
}

void osty_rt_list_push_i64(void *raw_list, int64_t value) {
    osty_rt_list_push_bytes(raw_list, &value, sizeof(value));
}

void osty_rt_list_push_i1(void *raw_list, bool value) {
    osty_rt_list_push_bytes(raw_list, &value, sizeof(value));
}

void osty_rt_list_push_f64(void *raw_list, double value) {
    osty_rt_list_push_bytes(raw_list, &value, sizeof(value));
}

void osty_rt_list_push_ptr(void *raw_list, void *value) {
    osty_rt_list_push_bytes(raw_list, &value, sizeof(value));
}

int64_t osty_rt_list_get_i64(void *raw_list, int64_t index) {
    int64_t value;
    memcpy(&value, osty_rt_list_get_bytes(raw_list, index, sizeof(value)), sizeof(value));
    return value;
}

bool osty_rt_list_get_i1(void *raw_list, int64_t index) {
    bool value;
    memcpy(&value, osty_rt_list_get_bytes(raw_list, index, sizeof(value)), sizeof(value));
    return value;
}

double osty_rt_list_get_f64(void *raw_list, int64_t index) {
    double value;
    memcpy(&value, osty_rt_list_get_bytes(raw_list, index, sizeof(value)), sizeof(value));
    return value;
}

void *osty_rt_list_get_ptr(void *raw_list, int64_t index) {
    void *value;
    memcpy(&value, osty_rt_list_get_bytes(raw_list, index, sizeof(value)), sizeof(value));
    return value;
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
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) {
    void *memory;
    (void)object_kind;
    (void)site;
    if (byte_size < 0) {
        osty_rt_abort("negative GC allocation size");
    }
    memory = calloc(1, (size_t)byte_size);
    if (memory == NULL) {
        osty_rt_abort("out of memory");
    }
    return memory;
}

void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) {
    (void)owner;
    (void)value;
    (void)slot_kind;
}

void osty_gc_root_bind_v1(void *root) {
    (void)root;
}

void osty_gc_root_release_v1(void *root) {
    (void)root;
}
