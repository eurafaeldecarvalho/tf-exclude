# 1999 independent instances plus one resource to exclude: 2000 managed resources.
resource "terraform_data" "keep" {
  count = 1999
  input = count.index
}

resource "terraform_data" "skip" {}
