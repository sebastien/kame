// Included after the exact generated main.c and shared native buffer helpers.
// Ledger: old TestName -> presentation_TestName (10 native cases).
// Two Run-only tests remain in presentation_test.go (9 invocations).
#include <assert.h>

static bool presentation_prefix(so_String text, const char *prefix) {
    size_t n = strlen(prefix);
    return text.len >= (so_int)n && memcmp(text.ptr, prefix, n) == 0;
}

static bool presentation_suffix(so_String text, const char *suffix) {
    size_t n = strlen(suffix);
    return text.len >= (so_int)n && memcmp(text.ptr + text.len - n, suffix, n) == 0;
}

static so_String presentation_trim_newline(bytes_Buffer *out) {
    so_String text = bytes_Buffer_String(out);
    if (text.len > 0 && text.ptr[text.len - 1] == '\n') text.len--;
    return text;
}

static so_String presentation_line(bytes_Buffer *out, int index) {
    so_String text = bytes_Buffer_String(out);
    so_int start = 0;
    for (so_int i = 0; i <= text.len; i++) {
        if (i == text.len || text.ptr[i] == '\n') {
            if (index-- == 0) return (so_String){text.ptr + start, i - start};
            start = i + 1;
        }
    }
    assert(!"missing presentation row");
    return (so_String){0};
}

static bytes_Buffer presentation_begin(void) {
    bytes_Buffer out = bytes_NewBuffer(mem_System, (so_Slice){0});
    selectedOutput = so_str("ansi");
    diagnosticColor = so_str("never");
    dashboardCommand = so_str("build");
    cliDiagnosticJSON = false;
    resetInvocationPresentation(native_writer(&out));
    return out;
}

static void presentation_end(bytes_Buffer *out) {
    freeInvocationPresentation(native_writer(out));
    selectedOutput = so_str("");
    diagnosticColor = so_str("never");
    dashboardCommand = so_str("");
    resetInvocationPresentation(native_writer(out));
    dashboardWidth = dashboardHeight = 0;
    dashboardOutput = (io_Writer){0};
    bytes_Buffer_Free(out);
}

static void presentation_TestDashboardLayoutAndGraphemeBounds(void) {
    bytes_Buffer out = presentation_begin();
    io_Writer writer = native_writer(&out);
    observeWorker((program_Event){.Kind = program_ProcessStarted, .Target = so_str("./build/main.o"), .Program = so_str("cc"), .RequestID = 7, .Generation = 1});
    so_at(cliWorker, workers, 0).Started = 0;
    buildProgress progress = {.Completed = 8};
    renderDashboard(writer, &progress, 80, 12, 1200000000);
    assert(native_equal(&out, "── build · workers: 1\n  1  [./build/main.o]                  running      cc                    1.2s\n8 complete · 0 failed · 1 running                              1.2s - building\n"));
    so_String line = presentation_line(&out, 1);
    assert(measureDisplayText(line, 1000).Cells == 78 && presentation_suffix(line, "1.2s"));
    clearDashboard(writer);
    bytes_Buffer_Reset(&out);
    renderDashboard(writer, &progress, 40, 4, 1200000000);
    assert(native_contains(&out, "main.o]") && !native_contains(&out, "cc") && presentation_suffix(bytes_Buffer_String(&out), "1.2s - building\n"));
    line = presentation_line(&out, 1);
    assert(measureDisplayText(line, 1000).Cells == 38 && presentation_suffix(line, "1.2s"));
    const char *graphemes[] = {"e\u0301", "界", "👩‍💻", "🇫🇷"};
    const char *fields[] = {"e\u0301xx", "界xx", "👩‍💻xx", "🇫🇷xx"};
    for (size_t i = 0; i < 4; i++) {
        bytes_Buffer_Reset(&out);
        writeDashboardField(writer, (so_String){fields[i], (so_int)strlen(fields[i])}, 3);
        assert(presentation_prefix(bytes_Buffer_String(&out), graphemes[i]));
    }
    observeWorker((program_Event){.Kind = program_ProcessExited, .Target = so_str("./build/main.o"), .RequestID = 8, .Generation = 1});
    assert(so_at(cliWorker, workers, 0).Target.len != 0);
    observeWorker((program_Event){.Kind = program_ProcessExited, .Target = so_str("./build/main.o"), .RequestID = 7, .Generation = 0});
    assert(so_at(cliWorker, workers, 0).Target.len != 0);
    observeWorker((program_Event){.Kind = program_TargetCompleted, .NodeID = 8, .Target = so_str("./build/main.o"), .Generation = 1, .Attempt = 2});
    assert(so_at(cliWorker, workers, 0).Target.len != 0);
    observeWorker((program_Event){.Kind = program_TargetCompleted, .Target = so_str("./build/main.o"), .Generation = 1, .Attempt = 2});
    assert(so_at(cliWorker, workers, 0).Target.len == 0);
    bytes_Buffer_Reset(&out);
    progress.Completed = 9;
    renderDashboard(writer, &progress, 80, 12, 1300000000);
    assert(dashboardRows == 2 && !native_contains(&out, "idle") && presentation_suffix(bytes_Buffer_String(&out), "1.3s - building\n"));
    bytes_Buffer_Reset(&out);
    char subject[201];
    for (int i = 0; i < 50; i++) memcpy(subject + 4 * i, "long", 4);
    subject[200] = 0;
    setDashboardSubject((so_String){subject, 200});
    progress = (buildProgress){0};
    renderDashboard(writer, &progress, 40, 4, 9999999999999);
    // Preserve Go len([]rune(line)), not terminal cell width.
    so_String text = bytes_Buffer_String(&out);
    int runes = 0;
    for (so_int i = 0; i < text.len; i++) {
        if (text.ptr[i] == '\n') { assert(runes <= 38); runes = 0; }
        else if (((unsigned char)text.ptr[i] & 0xc0) != 0x80) runes++;
    }
    assert(runes <= 38);
    presentation_end(&out);
}

static void presentation_TestActiveWorkersSurviveHistoricalSlots(void) {
    bytes_Buffer out = presentation_begin();
    io_Writer writer = native_writer(&out);
    for (int i = 0; i < 37; i++)
        observeWorker((program_Event){.Kind = program_ProcessStarted, .NodeID = i + 1, .Target = so_str("old"), .Program = so_str("cc"), .RequestID = i + 1, .Generation = 1});
    observeWorker((program_Event){.Kind = program_ProcessStarted, .NodeID = 38, .Target = so_str("current"), .RequestID = 38, .Generation = 1});
    for (int i = 0; i < 37; i++)
        observeWorker((program_Event){.Kind = program_ProcessExited, .NodeID = i + 1, .Target = so_str("old"), .RequestID = i + 1, .Generation = 1});
    buildProgress progress = {.Active = 30};
    renderDashboard(writer, &progress, 80, 12, 1000000000);
    assert(native_contains(&out, "[current]") && native_contains(&out, "process") && !native_contains(&out, "idle") && !native_contains(&out, "waiting") && dashboardRows == 3);
    bytes_Buffer_Reset(&out);
    observeWorker((program_Event){.Kind = program_ProcessExited, .NodeID = 38, .Target = so_str("current"), .RequestID = 38, .Generation = 1});
    renderDashboard(writer, &progress, 80, 12, 2000000000);
    assert(!native_contains(&out, "0 running") && !native_contains(&out, "waiting") && presentation_suffix(bytes_Buffer_String(&out), "2.0s - building\n"));
    presentation_end(&out);
}

static void presentation_TestDashboardEndTargetAtBottom(void) {
    bytes_Buffer out = presentation_begin();
    io_Writer writer = native_writer(&out);
    setDashboardSubject(so_str("default"));
    observeWorker((program_Event){.Kind = program_ProcessStarted, .Target = so_str("./build/main.o"), .Program = so_str("cc"), .RequestID = 1});
    buildProgress progress = {0};
    renderDashboard(writer, &progress, 80, 12, 1000000000);
    int lines = 1;
    so_String text = presentation_trim_newline(&out);
    for (so_int i = 0; i < text.len; i++) if (text.ptr[i] == '\n') lines++;
    assert(lines == 4 && so_string_eq(presentation_line(&out, 0), so_str("── build · workers: 1")));
    assert(strings_Contains(presentation_line(&out, 1), so_str("main.o")));
    assert(so_string_eq(presentation_line(&out, 2), so_str("target [default]")) && presentation_suffix(presentation_line(&out, 3), "1.0s - building"));
    bytes_Buffer_Reset(&out);
    renderDashboard(writer, &progress, 40, 4, 1000000000);
    assert(dashboardRows <= 4 && native_contains(&out, "target [default]\n"));
    presentation_end(&out);
}

static void presentation_TestDashboardUpdatesOnlyChangedRows(void) {
    bytes_Buffer out = presentation_begin();
    io_Writer writer = native_writer(&out);
    so_String first = so_str("workers\n  1 [compile] cc\n8 complete · 0 failed\n");
    paintDashboard(writer, first, 0, 3);
    bytes_Buffer_Reset(&out);
    paintDashboard(writer, first, 3, 3);
    assert(bytes_Buffer_Len(&out) == 0);
    paintDashboard(writer, so_str("workers\n  1 [compile] cc\n9 complete · 0 failed\n"), 3, 3);
    assert(native_equal(&out, "\x1b[1A\r9 complete · 0 failed\x1b[K\x1b[1B\r"));
    presentation_end(&out);
}

static void presentation_TestOutcomeColumnsAndRestrainedColor(void) {
    bytes_Buffer out = presentation_begin();
    io_Writer writer = native_writer(&out);
    char long_target[161];
    for (int i = 0; i < 40; i++) memcpy(long_target + 4 * i, "long", 4);
    long_target[160] = 0;
    const char *targets[] = {"short", long_target, "界👩‍💻e\u0301"};
    for (size_t i = 0; i < 3; i++) {
        bytes_Buffer_Reset(&out);
        writeOutcomeRow(writer, (so_String){targets[i], (so_int)strlen(targets[i])}, so_str("0.1s - ✓"), so_str("status.success"), 38);
        so_String line = presentation_trim_newline(&out);
        assert(measureDisplayText(line, 1000).Cells == 38 && presentation_suffix(line, "0.1s - ✓") && presentation_prefix(line, "["));
    }
    bytes_Buffer_Reset(&out);
    diagnosticColor = so_str("always");
    writeOutcomeRow(writer, so_str("target"), so_str("failed"), so_str("status.failed"), 0);
    assert(native_equal(&out, "\x1b[2m[\x1b[0m\x1b[35mtarget\x1b[0m\x1b[2m]\x1b[0m \x1b[1;31mfailed\x1b[0m\n"));
    diagnosticColor = so_str("never");
    presentation_end(&out);
}

static void presentation_TestTargetDurationUsesLifecycleClock(void) {
    bytes_Buffer out = presentation_begin();
    buildProgress progress = {0};
    program_Event event = {.Kind = program_TargetStarted, .Target = so_str("target"), .Key = {.Name = so_str("target")}, .NodeID = 7, .Generation = 1, .HasMonotonic = true, .MonotonicNS = 1000000000};
    observeOutcome(event, &progress);
    event.Kind = program_TargetCompleted; event.MonotonicNS = 1123000000;
    observeOutcome(event, &progress);
    writeTargetOutcome(native_writer(&out), event, so_str("done"), so_str("status.success"));
    assert(native_equal(&out, "[target] 0.1s - ✓\n"));
    event.Generation = 2; event.Kind = program_TargetStarted; event.MonotonicNS = 2000000000;
    observeOutcome(event, &progress);
    event.Kind = program_TargetCompleted; event.MonotonicNS = 2045000000;
    observeOutcome(event, &progress);
    bytes_Buffer_Reset(&out);
    writeTargetOutcome(native_writer(&out), event, so_str("done"), so_str("status.success"));
    assert(native_equal(&out, "[target] 0.0s - ✓\n"));
    presentation_end(&out);
}

static void presentation_TestDurationAndPathTheme(void) {
    bytes_Buffer out = presentation_begin();
    io_Writer writer = native_writer(&out);
    writeDuration(writer, 40840);
    assert(native_equal(&out, "40.8s"));
    bytes_Buffer_Reset(&out);
    writeOutcomeRow(writer, so_str("default"), so_str("40.8s - ✓"), so_str("status.success"), 78);
    assert(presentation_suffix(bytes_Buffer_String(&out), "40.8s - ✓\n") && measureDisplayText(presentation_trim_newline(&out), 1000).Cells == 78);
    bytes_Buffer_Reset(&out);
    writeBuildTally(writer, (buildProgress){.Completed = 122}, 0, 40840, so_str("✓"), so_str("status.success"), 78);
    assert(presentation_suffix(bytes_Buffer_String(&out), "40.8s - ✓\n") && measureDisplayText(presentation_trim_newline(&out), 1000).Cells == 78);
    bytes_Buffer_Reset(&out);
    diagnosticColor = so_str("always");
    writeOutcomeRow(writer, so_str("./dist/www/page.html"), so_str("40.8s - ✓"), so_str("status.success"), 0);
    assert(native_contains(&out, "\x1b[2m./dist/www/\x1b[0m\x1b[1mpage.html\x1b[0m") && native_contains(&out, "40.8s - \x1b[32m✓\x1b[0m"));
    bytes_Buffer_Reset(&out);
    diagnosticColor = so_str("never");
    writeTargetField(writer, so_str("./very/long/directory/page.html"), 16);
    assert(presentation_suffix(bytes_Buffer_String(&out), "/page.html") && measureDisplayText(bytes_Buffer_String(&out), 1000).Cells <= 16);
    presentation_end(&out);
}

static void presentation_TestOutcomeIdentityCountsOnce(void) {
    bytes_Buffer out = presentation_begin();
    buildProgress progress = {0};
    program_Event event = {.Kind = program_TargetStarted, .Target = so_str("default"), .NodeID = 3, .Generation = 1, .Key = {.Name = so_str("default")}};
    observeOutcome(event, &progress); observeOutcome(event, &progress);
    event.Kind = program_TargetCompleted;
    observeOutcome(event, &progress); observeOutcome(event, &progress);
    assert(progress.Active == 0 && progress.Completed == 1);
    event.Generation = 2;
    observeOutcome(event, &progress);
    assert(progress.Completed == 2);
    presentation_end(&out);
}

static void presentation_TestFailureSummaryDoesNotBecomeCancellation(void) {
    bytes_Buffer out = presentation_begin();
    cli_WriteSummary(native_writer(&out), so_str("build"), 1, false, 24, 8, 1, 2);
    assert(native_contains(&out, "\"status\":\"failure\""));
    presentation_end(&out);
}

static void presentation_TestOutcomeCleanupAllowsSameIdentityInNextCycle(void) {
    bytes_Buffer out = presentation_begin();
    program_Event event = {.Kind = program_TargetCompleted, .NodeID = 3, .Generation = 1, .Key = {.Name = so_str("default")}};
    buildProgress progress = {0};
    observeOutcome(event, &progress);
    freeOutcomes(); freeOutcomes();
    assert(outcomes.ptr == NULL);
    progress = (buildProgress){0};
    observeOutcome(event, &progress);
    assert(progress.Completed == 1);
    freeInvocationPresentation(native_writer(&out));
    assert(outcomes.ptr == NULL);
    presentation_end(&out);
}

static void test_cli_presentation(void) {
    presentation_TestDashboardLayoutAndGraphemeBounds();
    presentation_TestActiveWorkersSurviveHistoricalSlots();
    presentation_TestDashboardEndTargetAtBottom();
    presentation_TestDashboardUpdatesOnlyChangedRows();
    presentation_TestOutcomeColumnsAndRestrainedColor();
    presentation_TestTargetDurationUsesLifecycleClock();
    presentation_TestDurationAndPathTheme();
    presentation_TestOutcomeIdentityCountsOnce();
    presentation_TestFailureSummaryDoesNotBecomeCancellation();
    presentation_TestOutcomeCleanupAllowsSameIdentityInNextCycle();
}
