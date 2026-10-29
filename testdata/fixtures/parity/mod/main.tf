variable "prefix" {
  type    = string
  default = "child"
}

resource "terraform_data" "leaf" {
  input = var.prefix
}

resource "terraform_data" "uses_leaf" {
  input = terraform_data.leaf.output
}
