include "shared.thrift"

struct Holder {
  1: shared.User user,
  2: shared.User backup,
}
