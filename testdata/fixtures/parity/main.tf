resource "terraform_data" "a" {}

resource "terraform_data" "b" {
  input = terraform_data.a.output
}

resource "terraform_data" "c" {
  input = terraform_data.a.output
}

resource "terraform_data" "d" {
  input = {
    b = terraform_data.b.output
    c = terraform_data.c.output
  }
}

resource "terraform_data" "counted" {
  count = 3
  input = count.index
}

resource "terraform_data" "indexed_dep" {
  count = 3
  input = terraform_data.counted[count.index].output
}

resource "terraform_data" "each" {
  for_each = toset(["x", "y"])
  input    = each.key
}

locals {
  from_a = terraform_data.a.output
}

resource "terraform_data" "via_local" {
  input = local.from_a
}

resource "terraform_data" "explicit" {
  depends_on = [terraform_data.via_local]
}

module "child" {
  source = "./mod"
}

module "instances" {
  source = "./mod"
  count  = 2
  prefix = "i${count.index}"
}

module "foreach" {
  source   = "./modfe"
  for_each = toset(["red", "blue"])
}

module "wrap" {
  source = "./wrap"
}

resource "terraform_data" "uses_deep" {
  input = module.wrap.id
}
