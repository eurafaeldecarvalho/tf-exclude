# 200 managed resources: db, its dependent app, 98 pool instances,
# 50 in module.one (count) and 50 in module.two (for_each).
resource "terraform_data" "db" {}

resource "terraform_data" "app" {
  input = terraform_data.db.output
}

resource "terraform_data" "pool" {
  count = 98
  input = count.index
}

module "one" {
  source = "./mod_count"
  n      = 50
}

module "two" {
  source = "./mod_each"
  keys   = toset([for i in range(50) : format("k%03d", i)])
}
