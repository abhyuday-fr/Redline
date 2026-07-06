int main() {
  int *p = nullptr;
  *p = 42; // guaranteed segfault
  return 0;
}
