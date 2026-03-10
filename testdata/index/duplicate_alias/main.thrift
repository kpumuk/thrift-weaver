include "shared.thrift"
include "nested/shared.thrift"

struct Holder {
  1: shared.User user,
}
