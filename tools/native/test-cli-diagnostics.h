// Included after the exact generated main.c and the shared native_* helpers.
#pragma once
#include <assert.h>

typedef struct {
    unsigned char data[256];
    so_int n;
} native_cli_fixed_writer;

typedef struct {
    size_t allocations, reallocations, frees;
} native_cli_denied_attempts;

static so_R_ptr_err native_cli_deny_alloc(void *self, so_int size, so_int align) {
    native_cli_denied_attempts *attempts = self;
    attempts->allocations++;
    mem_NoAllocator denied = {0};
    return mem_NoAllocator_Alloc(&denied, size, align);
}

static so_R_ptr_err native_cli_deny_realloc(void *self, void *ptr, so_int old_size, so_int new_size, so_int align) {
    native_cli_denied_attempts *attempts = self;
    attempts->reallocations++;
    mem_NoAllocator denied = {0};
    return mem_NoAllocator_Realloc(&denied, ptr, old_size, new_size, align);
}

static void native_cli_deny_free(void *self, void *ptr, so_int size, so_int align) {
    native_cli_denied_attempts *attempts = self;
    attempts->frees++;
    (void)ptr; (void)size; (void)align;
}

static so_R_int_err native_cli_fixed_write(void *self, so_Slice data) {
    native_cli_fixed_writer *out = self;
    assert(data.len >= 0 && out->n + data.len <= (so_int)sizeof(out->data));
    memcpy(out->data + out->n, data.ptr, (size_t)data.len);
    out->n += data.len;
    return (so_R_int_err){.val = data.len};
}

static void test_cli_diagnostics(void) {
    // TestMergeEnvironmentOverridesExistingValue: stack slice, owned strings.
    so_String entries[] = {cloneCommandText(so_str("KM_CLI_TEST=old")),
        cloneCommandText(so_str("KEEP=value"))};
    so_String override[] = {so_str("KM_CLI_TEST=new")};
    so_Slice values = mergeEnvironment((so_Slice){entries, 2, 2},
        (so_Slice){override, 1, 1});
    assert(values.len == 2 && values.ptr == entries);
    assert(so_string_eq(entries[0], so_str("KM_CLI_TEST=new")));
    assert(so_string_eq(entries[1], so_str("KEEP=value")));
    for (so_int i = 0; i < values.len; i++)
        mem_FreeString(mem_System, so_at(so_String, values, i));

    // TestMergeEnvironmentUsesHostNameRules: heap slice can append on POSIX.
    values = slices_Make(so_String, (mem_System), (3));
    so_at(so_String, values, 0) = cloneCommandText(so_str("KM_CASE_TEST=old"));
    so_at(so_String, values, 1) = cloneCommandText(so_str("KEEP=value"));
    values.len = 2;
    override[0] = so_str("km_case_test=new");
    values = mergeEnvironment(values, (so_Slice){override, 1, 1});
    bool insensitive = posix_EnvironmentNameEqual(so_str("KM_CASE_TEST"), so_str("km_case_test"));
    assert(values.len == (insensitive ? 2 : 3));
    assert(so_string_eq(so_at(so_String, values, 0),
        insensitive ? so_str("km_case_test=new") : so_str("KM_CASE_TEST=old")));
    assert(so_string_eq(so_at(so_String, values, 1), so_str("KEEP=value")));
    if (!insensitive)
        assert(so_string_eq(so_at(so_String, values, 2), so_str("km_case_test=new")));
    for (so_int i = 0; i < values.len; i++)
        mem_FreeString(mem_System, so_at(so_String, values, i));
    slices_Free(so_String, (mem_System), (values));

    bytes_Buffer out = bytes_NewBuffer(mem_System, (so_Slice){0});
    // TestJSONEventEncodesBinaryChunk.
    so_byte binary[] = {0xff, 0x00};
    writeJSONEvent(native_writer(&out), (program_Event){.Kind = program_Stdout,
        .Target = so_str("build"), .Data = {binary, 2, 2}});
    assert(native_equal(&out, "{\"schema\":1,\"type\":\"stdout\",\"target\":\"build\",\"node\":0,\"generation\":0,\"attempt\":0,\"data\":\"/wA=\",\"encoding\":\"base64\"}\n"));

    // TestJSONEventIncludesStructuredRuntimeContext.
    bytes_Buffer_Reset(&out);
    writeJSONEvent(native_writer(&out), (program_Event){.Kind = program_DependencyDiscovered,
        .Target = so_str("build"), .Key = {.Kind = core_ResourceTask, .Name = so_str("build")},
        .DependencyID = 9, .DependencyKey = {.Kind = core_ResourceFile, .Name = so_str("./input")}});
    assert(native_contains(&out, "\"resource\":{\"kind\":\"task\",\"name\":\"build\"}"));
    assert(native_contains(&out, "\"dependency\":{\"node\":9,\"resource\":{\"kind\":\"file\",\"name\":\"./input\"}}"));
    bytes_Buffer_Reset(&out);
    writeJSONEvent(native_writer(&out), (program_Event){.Kind = program_TargetValue,
        .Target = so_str("result"), .Value = {.Kind = core_String, .Text = so_str("value")}});
    assert(native_contains(&out, "\"value\":{\"kind\":\"string\",\"data\":\"value\"}"));

    // TestJSONDiagnosticRetainsNotesFramesAndSeverity: all metadata is borrowed.
    bytes_Buffer_Reset(&out);
    so_String notes[] = {so_str("rebuild scheduled")};
    diagnostic_Frame warning_frames[] = {{.Label = so_str("rule"), .Span = {3, 7}}};
    writeJSONDiagnostic(native_writer(&out), (diagnostic_Diagnostic){
        .Code = so_str("CACHE_UNUSABLE"), .Severity = diagnostic_Warning,
        .Message = so_str("cache record ignored"), .Notes = {notes, 1, 1},
        .Frames = {warning_frames, 1, 1}});
    assert(native_contains(&out, "\"severity\":\"warning\""));
    assert(native_contains(&out, "\"notes\":[\"rebuild scheduled\"]"));
    assert(native_contains(&out, "\"frames\":[{\"label\":\"rule\"}]"));

    // TestDiagnosticJSONCarriesStructuredContext.
    bytes_Buffer_Reset(&out);
    diagnostic_Related related[] = {{.Message = so_str("declared here"),
        .Source = so_str("Makefile.kmk"), .Span = {0, 3}}};
    diagnostic_Frame frames[] = {{.Kind = so_str("rule"), .Label = so_str("build"),
        .Source = so_str("Makefile.kmk"), .Span = {0, 7}}};
    so_String stack[] = {so_str("all"), so_str("build")};
    so_String tips[] = {so_str("check the compiler")};
    writeJSONDiagnostic(native_writer(&out), (diagnostic_Diagnostic){
        .Code = so_str("RECIPE_FAIL"), .Severity = diagnostic_Error,
        .Message = so_str("recipe exited unsuccessfully"), .Source = so_str("Makefile.kmk"),
        .Span = {4, 7}, .Related = {related, 1, 1}, .Frames = {frames, 1, 1},
        .Target = so_str("build"), .TargetStack = {stack, 2, 2}, .Tips = {tips, 1, 1},
        .Cause = {.Kind = so_str("process"), .Message = so_str("shell exited"),
            .Program = so_str("sh"), .Status = 127, .HasStatus = true,
            .StderrTruncated = true, .StderrLimit = 64}});
    const char *fields[] = {"\"related\"", "\"kind\":\"rule\"",
        "\"targetStack\":[\"all\",\"build\"]", "\"tips\":[\"check the compiler\"]",
        "\"cause\":{\"kind\":\"process\"", "\"status\":127",
        "\"stderrTruncated\":true", "\"stderrLimit\":64"};
    for (size_t i = 0; i < sizeof(fields) / sizeof(fields[0]); i++)
        assert(native_contains(&out, fields[i]));

    // TestDiagnosticRendererUsesDisplayCellsAndNoSyntheticLocation.
    so_String old_color = diagnosticColor;
    so_String old_format = diagnosticFormat;
    diagnosticColor = so_str("never");
    diagnosticFormat = so_str("human");
    source_Source *src = source_New(mem_System, so_str("Makefile.kmk"), so_str("\t界e\u0301 bad\n"));
    bytes_Buffer_Reset(&out);
    cliDiagnosticWithSource(native_writer(&out), (diagnostic_Diagnostic){
        .Source = so_str("Makefile.kmk"), .Code = so_str("PARSE_ERR"),
        .Severity = diagnostic_Error, .Message = so_str("bad token"), .Span = {4, 8}}, src);
    assert(native_contains(&out, "Makefile.kmk:1:11:"));
    assert(native_contains(&out, "^~"));
    bytes_Buffer_Reset(&out);
    cliDiagnosticWithSource(native_writer(&out), (diagnostic_Diagnostic){
        .Code = so_str("OPT_UNKNOWN"), .Severity = diagnostic_Error,
        .Message = so_str("unknown option")}, src);
    assert(!native_contains(&out, "<command>"));
    assert(native_contains(&out, "error OPT_UNKNOWN"));
    source_Source_Free(src, mem_System);

    // TestDiagnosticRendererWrapsSourceAtExplicitWidth, including zero-width EOF.
    src = source_New(mem_System, so_str("inline.kmk"), so_str("01234567890123456789x"));
    diagnostic_Diagnostic wrapped = {.Source = so_str("inline.kmk"), .Code = so_str("PARSE_ERR"),
        .Severity = diagnostic_Error, .Message = so_str("bad token"), .Span = {20, 21}};
    bytes_Buffer_Reset(&out);
    cliDiagnosticWithSourceWidth(native_writer(&out), wrapped, src, 20);
    assert(native_contains(&out, "01234567890123456789\nx\n^\n"));
    wrapped.Span = (diagnostic_Span){21, 21};
    bytes_Buffer_Reset(&out);
    cliDiagnosticWithSourceWidth(native_writer(&out), wrapped, src, 20);
    assert(native_contains(&out, "inline.kmk:1:22:"));
    assert(native_contains(&out, "01234567890123456789\nx\n ^\n"));
    source_Source_Free(src, mem_System);
    diagnosticColor = old_color;
    diagnosticFormat = old_format;
    bytes_Buffer_Free(&out);

    // TestDiagnosticPresentationEnvironmentAndJSONEarlyErrors: direct-helper portion only.
    so_String no_color[] = {so_str("NO_COLOR=1")};
    so_String force_color[] = {so_str("CLICOLOR_FORCE=1")};
    assert(so_string_eq(resolveDiagnosticColor(so_str("auto"), so_str("human"), true,
        (so_Slice){no_color, 1, 1}), so_str("never")));
    assert(so_string_eq(resolveDiagnosticColor(so_str("auto"), so_str("human"), false,
        (so_Slice){force_color, 1, 1}), so_str("always")));
    assert(so_string_eq(resolveDiagnosticColor(so_str("always"), so_str("plain"), true,
        (so_Slice){0}), so_str("never")));

    // TestEmergencyDiagnosticNeedsNoAllocator: deny allocations and count attempts.
    io_Writer old_out = cliDiagnosticOut;
    bool old_json = cliDiagnosticJSON;
    mem_Allocator original = mem_System;
    native_cli_denied_attempts attempts = {0};
    so_R_ptr_err allocation_probe = native_cli_deny_alloc(&attempts, 1, 1);
    so_R_ptr_err reallocation_probe = native_cli_deny_realloc(&attempts, NULL, 1, 2, 1);
    native_cli_deny_free(&attempts, NULL, 0, 1);
    assert(!allocation_probe.val && allocation_probe.err.self && attempts.allocations == 1);
    assert(!reallocation_probe.val && reallocation_probe.err.self && attempts.reallocations == 1);
    assert(attempts.frees == 1);
    attempts = (native_cli_denied_attempts){0};
    mem_System = (mem_Allocator){.self = &attempts, .Alloc = native_cli_deny_alloc,
        .Realloc = native_cli_deny_realloc, .Free = native_cli_deny_free};
    native_cli_fixed_writer plain = {0}, json = {0};
    io_Writer plain_writer = {.self = &plain, .Write = native_cli_fixed_write};
    cliDiagnosticOut = (io_Writer){0};
    cliDiagnosticJSON = false;
    writeEmergencyDiagnostic(plain_writer, false);
    const char *plain_text = "fatal NO_MEMORY: memory exhausted\n";
    assert(plain.n == (so_int)strlen(plain_text));
    assert(memcmp(plain.data, plain_text, (size_t)plain.n) == 0);
    cliDiagnosticOut = (io_Writer){.self = &json, .Write = native_cli_fixed_write};
    cliDiagnosticJSON = true;
    writeEmergencyDiagnostic(plain_writer, true);
    const char *json_text = "{\"schema\":1,\"type\":\"diagnostic\",\"diagnostic\":{\"code\":\"NO_MEMORY\",\"severity\":\"fatal\",\"message\":\"memory exhausted\"}}\n";
    assert(json.n == (so_int)strlen(json_text));
    assert(memcmp(json.data, json_text, (size_t)json.n) == 0);
    assert(plain.n == (so_int)strlen(plain_text));
    assert(attempts.allocations == 0 && attempts.reallocations == 0 && attempts.frees == 0);
    mem_System = original;
    cliDiagnosticOut = old_out;
    cliDiagnosticJSON = old_json;
}
