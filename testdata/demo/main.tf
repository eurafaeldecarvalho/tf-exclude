resource "terraform_data" "db" {}

resource "terraform_data" "app" {
  input = terraform_data.db.output
}

resource "terraform_data" "api" {}

resource "terraform_data" "web" {
  count = 2
  input = count.index
}
