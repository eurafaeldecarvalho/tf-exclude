variable "n" {
  type = number
}

resource "terraform_data" "item" {
  count = var.n
  input = count.index
}
