#define CATCH_CONFIG_MAIN
#include <catch2/catch_test_macros.hpp>

int add(int a, int b) { return a + b; }

TEST_CASE("addition works", "[math]") {
  REQUIRE(add(2, 3) == 999); // deliberately wrong
}
