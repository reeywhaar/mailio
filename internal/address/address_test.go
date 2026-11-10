package address

import "testing"

func TestAnAddressIsLowercasedAndSplitAtThePlus(t *testing.T) {
	a, err := Parse("  Inbox+Shop.Amazon@Reey.RU. ")
	if err != nil {
		t.Fatal(err)
	}
	if a.String() != "inbox+shop.amazon@reey.ru" {
		t.Errorf("String() = %q", a.String())
	}
	if a.Name() != "inbox" || a.Tag() != "shop.amazon" {
		t.Errorf("Name() = %q, Tag() = %q", a.Name(), a.Tag())
	}
}

func TestOnlyTheFirstPlusSeparates(t *testing.T) {
	a, err := Parse("inbox+a+b@reey.ru")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "inbox" || a.Tag() != "a+b" {
		t.Errorf("Name() = %q, Tag() = %q", a.Name(), a.Tag())
	}
}

func TestAnAddressWithoutATagIsItsOwnName(t *testing.T) {
	a, err := Parse("inbox@reey.ru")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "inbox" || a.Tag() != "" {
		t.Errorf("Name() = %q, Tag() = %q", a.Name(), a.Tag())
	}
}

func TestWhatIsNotAnAddressIsRefused(t *testing.T) {
	for _, s := range []string{
		"",
		"inbox",
		"@reey.ru",
		"inbox@",
		"inbox@reey",
		"in box@reey.ru",
		`"in box"@reey.ru`,
		".inbox@reey.ru",
		"inbox.@reey.ru",
		"in..box@reey.ru",
		"inbox@[127.0.0.1]",
		"inbox@127.0.0.1",
		"inbox@-reey.ru",
		"inbox@reey..ru",
		"inbox@reey_ru.com",
		"пчёлка@reey.ru",
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded", s)
		}
	}
}

func TestAnAccountCannotBeRegisteredUnderATaggedAddress(t *testing.T) {
	if _, err := ParseAccount("inbox+shop@reey.ru"); err == nil {
		t.Error("ParseAccount accepted a +")
	}
	if _, err := ParseAccount("inbox@reey.ru"); err != nil {
		t.Error(err)
	}
}
