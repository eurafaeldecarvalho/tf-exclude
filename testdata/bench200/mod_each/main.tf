variable "keys" {
  type = set(string)
}

resource "terraform_data" "item" {
  for_each = var.keys
  input    = each.key
}
