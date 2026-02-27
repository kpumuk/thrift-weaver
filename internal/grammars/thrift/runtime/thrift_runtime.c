#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "tree_sitter/api.h"

TSLanguage *tree_sitter_thrift(void);

uintptr_t tw_parser_new(void) {
  return (uintptr_t)ts_parser_new();
}

void tw_parser_delete(uintptr_t parser) {
  ts_parser_delete((TSParser *)parser);
}

uint32_t tw_parser_set_language(uintptr_t parser) {
  return ts_parser_set_language((TSParser *)parser, tree_sitter_thrift()) ? 1 : 0;
}

uintptr_t tw_parser_parse_string(uintptr_t parser, uintptr_t old_tree, const char *src, uint32_t len) {
  return (uintptr_t)ts_parser_parse_string((TSParser *)parser, (TSTree *)old_tree, src, len);
}

void tw_tree_delete(uintptr_t tree) {
  ts_tree_delete((TSTree *)tree);
}

void tw_tree_root_node(uintptr_t tree, TSNode *out_node) {
  *out_node = ts_tree_root_node((TSTree *)tree);
}

uint32_t tw_node_child_count(const TSNode *node) {
  return ts_node_child_count(*node);
}

void tw_node_child(const TSNode *node, uint32_t index, TSNode *out_node) {
  *out_node = ts_node_child(*node, index);
}

uint32_t tw_node_named_child_count(const TSNode *node) {
  return ts_node_named_child_count(*node);
}

void tw_node_named_child(const TSNode *node, uint32_t index, TSNode *out_node) {
  *out_node = ts_node_named_child(*node, index);
}

uintptr_t tw_node_type(const TSNode *node) {
  return (uintptr_t)ts_node_type(*node);
}

uint32_t tw_node_symbol(const TSNode *node) {
  return (uint32_t)ts_node_symbol(*node);
}

uint32_t tw_node_start_byte(const TSNode *node) {
  return ts_node_start_byte(*node);
}

uint32_t tw_node_end_byte(const TSNode *node) {
  return ts_node_end_byte(*node);
}

uint32_t tw_node_is_error(const TSNode *node) {
  return ts_node_is_error(*node) ? 1 : 0;
}

uint32_t tw_node_is_missing(const TSNode *node) {
  return ts_node_is_missing(*node) ? 1 : 0;
}

uint32_t tw_node_is_named(const TSNode *node) {
  return ts_node_is_named(*node) ? 1 : 0;
}

uint32_t tw_node_is_extra(const TSNode *node) {
  return ts_node_is_extra(*node) ? 1 : 0;
}

uint32_t tw_node_has_error(const TSNode *node) {
  return ts_node_has_error(*node) ? 1 : 0;
}
