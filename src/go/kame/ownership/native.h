#ifndef KAME_OWNERSHIP_H
#define KAME_OWNERSHIP_H
typedef struct km_own_store km_own_store;
typedef struct km_own_file km_own_file;
bool km_own_supported(void);
km_own_store *km_own_open(so_String, bool, bool, int *);
void km_own_close(km_own_store *);
int km_own_root(km_own_store *, so_Slice);
km_own_file *km_own_read(km_own_store *, so_String, bool, int *);
int64_t km_own_size(km_own_file *);
int km_own_stamp(km_own_file *, so_Slice);
int km_own_bytes(km_own_file *, so_Slice);
bool km_own_stable(km_own_file *);
void km_own_file_close(km_own_file *);
int km_own_write(km_own_store *, so_String, so_Slice);
int km_own_remove(km_own_store *, so_String);
int km_own_list(km_own_store *, so_Slice);
int km_own_capture(km_own_store *, so_String, so_String, so_Slice);
int km_own_restore(km_own_store *, so_String, so_String);
#endif
