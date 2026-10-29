resource "terraform_data" "a" {}

locals {
  b = terraform_data.a.output
}

resource "terraform_data" "c" {
  input = local.b
}

resource "terraform_data" "explicit" {
  depends_on = [terraform_data.c]
}
