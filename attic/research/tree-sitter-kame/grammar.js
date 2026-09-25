const rules = {
  source_file: ($) => repeat($._line),
  _line: ($) => choice($._definition, $.comment),
  _definition: ($) => choice($.value_definition),

  //  value_definition: ($) => prec.left(seq($.name, "=", repeat($._value))),
  value_definition: ($) => prec.left(seq($.name, "=", repeat($._value))),

  expr_chain: ($) => seq($._value, repeat(seq("|", $._value))),

  _value: ($) => choice($._expr, $._deref, $.number, $.string, $.path, $.name),

  _expr: ($) => choice($.expr_parens, $.expr_chain),

  expr_parens: ($) => prec(2, seq("(", repeat($._value), ")")),
  expr_chain: ($) => prec.left(seq($._value, "|", $._value)),

  name: ($) =>
    prec(10, seq($.identifier, optional(repeat1(seq(".", $.identifier))))),

  _deref: ($) => choice($.deref_name, $.deref_cmd, $.deref_expr),
  deref_name: ($) => seq("@", $.name),
  deref_expr: ($) => seq("@{", $.name, "}"),
  deref_cmd: ($) => seq("@", $.cmd),

  _eval: ($) => choice($.eval_expr),
  eval_expr: ($) => seq("@(", $._expr, ")"),

  comment: ($) => seq("#", /[^\n]*[\n$]/),
  string: ($) =>
    choice(
      seq("'", repeat(/[^\\']/), "'"), // Single-quoted string
      seq('"', repeat(/[^\\"]/), '"') // Double-quoted string
    ),

  // --
  // ## Paths
  path: ($) => seq($.path_prefix, repeat1($.path_segment)),
  path_prefix: ($) => choice("/", "./", seq($.protocol, "://")),

  // --
  // ## Tokens
  identifier: ($) => /[a-z]+/,
  word: ($) => /XXX[^\s]+/,
  number: ($) => /\d+/,
  protocol: ($) => /[a-zA-Z]+/, // Simple protocol name (letters only)
  path_segment: ($) => /[^\/]+/, // Any non-slash characters
  cmd: ($) => /`[a-zA-Z0-9_-]+`/,
};

module.exports = grammar({
  name: "kame",
  rules: rules,
  extras: ($) => [/[\s\t\r\n]/],
});
