/** Service docs */
service Demo {
  // method comment
  void ping(1: i32 id); // trailing signature comment
}

# Top-level hash comment
struct Holder {
  1: string value // trailing field comment
  # section header
  /* block comment */
  2: optional string note
}
