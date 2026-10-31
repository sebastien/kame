// Preserve the direct AST error-envelope golden; the public CLI deliberately
// presents parse failures as diagnostic events instead of partial AST output.
static size_t ast_fixture(const char *filename, char *data, size_t capacity) {
    FILE *file = fopen(filename, "rb");
    assert(file);
    size_t length = fread(data, 1, capacity, file);
    assert(!ferror(file));
    assert(length < capacity || fgetc(file) == EOF);
    assert(fclose(file) == 0);
    return length;
}

static void test_cli_invalid_ast(void) {
    char source_text[8192], expected[8192];
    size_t source_length = ast_fixture("src/go/kame/cmd/kame/testdata/parse/invalid-expr.km", source_text, sizeof(source_text));
    size_t expected_length = ast_fixture("src/go/kame/cmd/kame/testdata/parse/invalid-expr.json", expected, sizeof(expected));
    bytes_Buffer output = bytes_NewBuffer(mem_System, (so_Slice){0});
    so_int status = writeAST(native_writer(&output), so_str("expr"),
        so_str("testdata/parse/invalid-expr.km"), (so_String){source_text, (so_int)source_length});
    assert(status == 1);
    so_String actual = bytes_Buffer_String(&output);
    assert(actual.len == (so_int)expected_length);
    assert(memcmp(actual.ptr, expected, expected_length) == 0);
    bytes_Buffer_Free(&output);
}
