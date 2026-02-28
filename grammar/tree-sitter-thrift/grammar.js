const PREC = {
  annotation: 1,
};

module.exports = grammar({
  name: 'thrift',

  extras: ($) => [/[\s\uFEFF\u2060]+/, $.comment],

  word: ($) => $.identifier,

  inline: ($) => [
    $.header,
    $.definition,
    $.misplaced_header,
    $.definition_tail,
    $.misplaced_section,
  ],

  rules: {
    source_file: ($) => seq(repeat($.header), optional($.definition_tail)),

    definition_tail: ($) => seq(repeat1($.definition), optional($.misplaced_section)),

    misplaced_section: ($) => seq(repeat1($.misplaced_header), repeat($.definition)),

    comment: () =>
      token(
        choice(
          seq('//', /[^\r\n]*/),
          seq('#', /[^\r\n]*/),
          seq('/*', /[^*]*\*+([^/*][^*]*\*+)*/, '/'),
        ),
      ),

    header: ($) =>
      choice(
        $.include_declaration,
        $.cpp_include_declaration,
        $.namespace_declaration,
      ),

    definition: ($) =>
      choice(
        $.typedef_declaration,
        $.const_declaration,
        $.enum_definition,
        $.senum_definition,
        $.struct_definition,
        $.union_definition,
        $.exception_definition,
        $.service_definition,
      ),

    misplaced_header: ($) =>
      choice(
        alias($.include_declaration, $.misplaced_include_declaration),
        alias($.cpp_include_declaration, $.misplaced_cpp_include_declaration),
        alias($.namespace_declaration, $.misplaced_namespace_declaration),
      ),

    include_declaration: ($) => seq('include', field('path', $.string_literal), optional($._statement_sep)),

    cpp_include_declaration: ($) => seq('cpp_include', field('path', $.string_literal), optional($._statement_sep)),

    namespace_declaration: ($) =>
      choice(
        seq(
          'namespace',
          field('scope', $.namespace_named_scope),
          field('target', $.namespace_target),
          optional($.annotations),
          optional($._statement_sep),
        ),
        seq('namespace', field('scope', '*'), field('target', $.namespace_target), optional($._statement_sep)),
      ),

    namespace_named_scope: () => token(/[A-Za-z_][A-Za-z0-9_.]*/),
    namespace_target: () => token(/[A-Za-z_][A-Za-z0-9_.-]*/),

    typedef_declaration: ($) =>
      seq('typedef', field('type', $.type), field('name', $.identifier), optional($.annotations), optional($._statement_sep)),

    const_declaration: ($) =>
      seq(
        'const',
        field('type', $.type),
        field('name', $.identifier),
        '=',
        field('value', $.const_value),
        optional($.annotations),
        optional($._statement_sep),
      ),

    enum_definition: ($) =>
      seq(
        'enum',
        field('name', $.identifier),
        field('body', $.enum_block),
        optional($.annotations),
        optional($._statement_sep),
      ),

    enum_block: ($) => seq('{', repeat($.enum_value), '}'),

    enum_value: ($) =>
      seq(
        field('name', $.identifier),
        optional(seq('=', field('value', $.int_literal))),
        optional($.annotations),
        optional($._statement_sep),
      ),

    senum_definition: ($) =>
      seq(
        'senum',
        field('name', $.identifier),
        '{',
        repeat($.senum_value),
        '}',
        optional($.annotations),
        optional($._statement_sep),
      ),

    senum_value: ($) => seq(field('value', $.string_literal), optional($._statement_sep)),

    struct_definition: ($) => seq('struct', field('name', $.identifier), optional('xsd_all'), field('body', $.field_block), optional($.annotations), optional($._statement_sep)),
    union_definition: ($) => seq('union', field('name', $.identifier), optional('xsd_all'), field('body', $.field_block), optional($.annotations), optional($._statement_sep)),
    exception_definition: ($) => seq('exception', field('name', $.identifier), field('body', $.field_block), optional($.annotations), optional($._statement_sep)),

    field_block: ($) => seq('{', repeat($.field), '}'),

    field: ($) =>
      seq(
        optional(seq(field('id', $.field_id), ':')),
        optional(field('requiredness', $.requiredness)),
        field('type', $.type),
        optional(field('reference', $.field_reference)),
        field('name', $.field_name),
        optional(seq('=', field('default', $.const_value))),
        optional($.xsd_optional),
        optional($.xsd_nillable),
        optional($.xsd_attrs),
        optional($.annotations),
        optional($._statement_sep),
      ),

    field_id: ($) => $.int_literal,

    requiredness: () => choice('required', 'optional'),
    field_reference: () => 'reference',
    field_name: ($) => choice($.identifier, $.field_keyword_name),
    field_keyword_name: () =>
      choice(
        'namespace',
        'cpp_include',
        'include',
        'void',
        'bool',
        'byte',
        'i8',
        'i16',
        'i32',
        'i64',
        'double',
        'string',
        'binary',
        'uuid',
        'map',
        'list',
        'set',
        'oneway',
        'async',
        'typedef',
        'struct',
        'union',
        'exception',
        'extends',
        'throws',
        'service',
        'enum',
        'const',
        'required',
        'optional',
      ),

    xsd_optional: () => 'xsd_optional',
    xsd_nillable: () => 'xsd_nillable',
    xsd_attrs: ($) => seq('xsd_attrs', '{', repeat($.field), '}'),

    service_definition: ($) =>
      seq(
        'service',
        field('name', $.identifier),
        optional(seq('extends', field('super', $.scoped_identifier))),
        field('body', $.function_block),
        optional($.annotations),
        optional($._statement_sep),
      ),

    function_block: ($) => seq('{', repeat($.function_definition), '}'),

    function_definition: ($) =>
      seq(
        optional(field('modifier', $.function_modifier)),
        field('return_type', $.return_type),
        field('name', $.identifier),
        field('parameters', $.parameter_list),
        optional(field('throws', $.throws_clause)),
        optional($.annotations),
        optional($._statement_sep),
      ),

    function_modifier: () => choice('oneway', 'async'),
    return_type: ($) => choice('void', $.type),

    parameter_list: ($) => seq('(', repeat($.field), ')'),
    throws_clause: ($) => seq('throws', field('parameters', $.parameter_list)),

    type: ($) => choice($.base_type, $.map_type, $.list_type, $.set_type, $.scoped_identifier),

    base_type: () =>
      choice(
        'bool',
        'byte',
        'i8',
        'i16',
        'i32',
        'i64',
        'double',
        'string',
        'binary',
        'uuid',
      ),

    map_type: ($) =>
      seq(
        'map',
        optional(field('cpp_type_prefix', $.legacy_cpp_type)),
        '<',
        field('key', $.type),
        ',',
        field('value', $.type),
        '>',
      ),

    list_type: ($) =>
      seq(
        'list',
        optional(field('cpp_type_prefix', $.legacy_cpp_type)),
        '<',
        field('element', $.type),
        '>',
        optional(field('cpp_type_suffix', $.legacy_cpp_type)),
      ),
    set_type: ($) => seq('set', optional(field('cpp_type_prefix', $.legacy_cpp_type)), '<', field('element', $.type), '>'),

    legacy_cpp_type: ($) => seq('cpp_type', $.string_literal),

    annotations: ($) => prec.right(PREC.annotation, seq('(', repeat($.annotation), ')')),

    annotation: ($) =>
      seq(
        field('name', $.identifier),
        optional(seq('=', field('value', $.annotation_value))),
        optional($._statement_sep),
      ),
    annotation_value: ($) => $.string_literal,

    const_value: ($) =>
      choice(
        $.int_literal,
        $.float_literal,
        $.uuid_literal,
        $.string_literal,
        $.bool_literal,
        $.const_list,
        $.const_map,
        $.scoped_identifier,
      ),

    const_list: ($) =>
      seq('[', optional(seq($.const_value, repeat(seq($._list_sep, $.const_value)), optional($._list_sep))), ']'),
    const_map: ($) =>
      seq('{', optional(seq($.const_map_entry, repeat(seq($._list_sep, $.const_map_entry)), optional($._list_sep))), '}'),
    const_map_entry: ($) => seq(field('key', $.const_value), ':', field('value', $.const_value)),

    bool_literal: () => choice('true', 'false'),
    int_literal: () => token(choice(/[+-]?0[xX][0-9A-Fa-f]+/, /[+-]?[0-9]+/)),
    float_literal: () =>
      token(
        choice(
          /[+-]?[0-9]+\.[0-9]+([eE][+-]?[0-9]+)?/,
          /[+-]?[0-9]+[eE][+-]?[0-9]+/,
          /[+-]?\.[0-9]+([eE][+-]?[0-9]+)?/,
        ),
      ),

    string_literal: () =>
      token(
        choice(
          seq('"', repeat(choice(/[^"\\\r\n]+/, /\\./)), '"'),
          seq("'", repeat(choice(/[^'\\\r\n]+/, /\\./)), "'"),
        ),
      ),
    uuid_literal: () =>
      token(
        choice(
          /"[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}"/,
          /'[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}'/,
          /"\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}"/,
          /'\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}'/,
        ),
      ),

    identifier: () => /[A-Za-z_][A-Za-z0-9_]*/,
    scoped_identifier: ($) => seq($.identifier, repeat(seq('.', $.identifier))),

    _list_sep: () => choice(',', ';'),
    _statement_sep: () => choice(',', ';'),
  },
});
