#include <cstdio>

volatile long sink = 0;

long fib(int n) {
  if (n < 2)
    return n;
  return fib(n - 1) + fib(n - 2);
}

int main() {
  for (int i = 0; i < 5; i++) {
    sink += fib(32);
  }
  printf("done: %ld\n", sink);
  return 0;
}
