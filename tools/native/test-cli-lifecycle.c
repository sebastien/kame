// Compile against `so translate ./cmd/kame` output, excluding its main.c.
// This includes that exact generated CLI and repeatedly calls Run in one
// process, with the actual C allocator and explicit ownership accounting.
#define main kame_process_entrypoint
#include "main.c"
#undef main
#include <assert.h>

static io_Writer native_writer(bytes_Buffer *buffer) {
    return (io_Writer){.self = buffer, .Write = bytes_Buffer_Write};
}

static so_String native_string(const char *text) {
    return (so_String){text, (so_int)strlen(text)};
}

static bool native_equal(bytes_Buffer *buffer, const char *text) {
    return so_string_eq(bytes_Buffer_String(buffer), native_string(text));
}

static bool native_contains(bytes_Buffer *buffer, const char *text) {
    return strings_Contains(bytes_Buffer_String(buffer), native_string(text));
}

#include "test-cli-diagnostics.h"
#include "test-cli-presentation.h"
#include "test-cli-ast.h"

static void test_cli_build_source_diagnostics(void) {
    so_String arguments[] = {so_str("do"), so_str("plan"), so_str("--output"), so_str("text"),
        so_str("-c"), so_str("# heading\n(if :false (include ./absent.kmk) :nil)\ntask build :\ntask build :"),
        so_str("build")};
    bytes_Buffer input = bytes_NewBuffer(mem_System, (so_Slice){0});
    bytes_Buffer output = bytes_NewBuffer(mem_System, (so_Slice){0});
    bytes_Buffer errors = bytes_NewBuffer(mem_System, (so_Slice){0});
    so_int status = main_Run((so_Slice){arguments, 7, 7},
        (io_Reader){.self = &input, .Read = bytes_Buffer_Read},
        native_writer(&output), native_writer(&errors));
    assert(status == 1 && bytes_Buffer_Len(&output) == 0);
    assert(native_contains(&errors, "TGT_AMBIG"));
    assert(native_contains(&errors, "<command>:4:6:"));
    assert(native_contains(&errors, "task build :\n     ^"));
    bytes_Buffer_Free(&input);
    bytes_Buffer_Free(&output);
    bytes_Buffer_Free(&errors);
}

int main(int argc, char **argv) {
    so_String process_args[argc];
    so_args_init(argc, argv, process_args);
    posix_InstallSignals();
    mem_Allocator original = mem_System;
    mem_Tracker tracker = {.Allocator = original};
    mem_System = (mem_Allocator){.self = &tracker, .Alloc = mem_Tracker_Alloc,
        .Realloc = mem_Tracker_Realloc, .Free = mem_Tracker_Free};
    const char *cases[][16] = {
        {"-c", "task first :\ntask second :"},
        {"do", "run", "--lang", "expr", "--allow-read=/tmp", "--allow-env=HOME", "-c", "(nop 42)", "--", "left", "right"},
        {"do", "plan", "-c", "task build :\n\techo ignored", "build"},
        {"do", "span", "--expand", "-c", "SOURCE = ./input\ntask build : @(SOURCE)\n\ttrue", "build"},
        {"do", "fmt", "--lang", "expr"},
        {"--shell", "/bin/sh", "--env", "KM_TEST=value", "--unknown"},
        {"do", "run", "--lang", "expr", "--allow-read=/tmp", "--allow-env=HOME", "--unknown"},
        {"do", "fmt", "-i", "-n", "source.kmk"},
        {"do", "inputs", "build", "--depth", "invalid"},
        {"do", "run", "--lang", "km", "-c", "(if :false (include ./absent.km) :nil)"},
        {"do", "run", "--lang", "km", "-c", "(include ./absent.km)"},
        {"do", "run", "--lang", "km", "-c", "unused = (include ./absent.km)\n42"},
        {"do", "run", "--lang", "km", "-c", "include ./absent.km"},
        {"do", "run", "--lang", "expr", "-c", "(let [root \"./src\"] (match? @(root)/{name:*} \"./src/file\"))"},
        {"do", "run", "--lang", "expr", "-c", "(let [root \"./src\" output \"./build\"] (sub @(root)/{name:*} @(output)/{name}.o \"./src/file\"))"},
        {"do", "run", "--lang", "expr", "-c", "./{name:@(nop \"*\")}"},
        {"do", "parse", "--lang", "kash", "-c", "$x/cp -pL -- $inputs.0 $outputs.0"},
        {"do", "parse", "--lang", "expr", "--check", "-c", "42"},
        {"do", "fmt", "--lang", "expr", "-c", "[1   2]"},
        {"do", "fmt", "--lang", "expr", "-f", "missing.km", "-C", "."},
        {"do", "tools", "-C", ".", "check", "-c", "default :", "default", "--json"},
        {"do", "tools", "unexpected"},
        {"do", "tools", "check"},
        {"do", "cache"},
        {"do", "cache", "list", "clean"},
        {"do", "cache", "list", "--dry-run"},
    };
    const so_int statuses[] = {1, 0, 0, 0, 0, 2, 2, 2, 2, 0, 1, 1, 1, 0, 0, 1, 0, 0, 0, 1, 0, 2, 2, 2, 2, 2};
    for (int iteration = 0; iteration < 256; iteration++) {
        test_cli_invalid_ast();
        mem_Stats ast_stats = mem_Tracker_Stats(&tracker);
        assert(ast_stats.Alloc == 0 && ast_stats.Mallocs == ast_stats.Frees);
        test_cli_diagnostics();
        test_cli_build_source_diagnostics();
        mem_Stats diagnostic_stats = mem_Tracker_Stats(&tracker);
        assert(diagnostic_stats.Alloc == 0 && diagnostic_stats.Mallocs == diagnostic_stats.Frees);
        test_cli_presentation();
        mem_Stats presentation_stats = mem_Tracker_Stats(&tracker);
        assert(presentation_stats.Alloc == 0 && presentation_stats.Mallocs == presentation_stats.Frees);
        for (size_t test = 0; test < sizeof(cases) / sizeof(cases[0]); test++) {
            so_String arguments[16];
            so_int count = 0;
            while (cases[test][count]) {
                arguments[count] = (so_String){cases[test][count], (so_int)strlen(cases[test][count])};
                count++;
            }
            bytes_Buffer input = bytes_NewBufferString(mem_System, so_str("42\n"));
            bytes_Buffer output = bytes_NewBuffer(mem_System, (so_Slice){0});
            bytes_Buffer errors = bytes_NewBuffer(mem_System, (so_Slice){0});
            so_int status = main_Run((so_Slice){arguments, count, count},
                (io_Reader){.self = &input, .Read = bytes_Buffer_Read},
                (io_Writer){.self = &output, .Write = bytes_Buffer_Write},
                (io_Writer){.self = &errors, .Write = bytes_Buffer_Write});
            assert(status == statuses[test]);
            bytes_Buffer_Free(&output);
            bytes_Buffer_Free(&errors);
            mem_Stats stats = mem_Tracker_Stats(&tracker);
            if (stats.Alloc != 0 || stats.Mallocs != stats.Frees) {
                fprintf(stderr, "CLI case %zu (%s): %lld live bytes\n", test, cases[test][0], (long long)stats.Alloc);
            }
            assert(stats.Alloc == 0 && stats.Mallocs == stats.Frees);
        }
    }
    mem_System = original;
    puts("PASS compiled Solod CLI: native diagnostic/presentation/source cases, zero live allocations");
    return 0;
}
