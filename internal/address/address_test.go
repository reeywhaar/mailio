package address

import "testing"

func TestAnAddressIsLowercasedAndSplitAtThePlus(t *testing.T) {
	a, err := Parse("  Inbox+Shop.Amazon@Example.ORG. ")
	if err != nil {
		t.Fatal(err)
	}
	if a.String() != "inbox+shop.amazon@example.org" {
		t.Errorf("String() = %q", a.String())
	}
	if a.Name() != "inbox" || a.Tag() != "shop.amazon" {
		t.Errorf("Name() = %q, Tag() = %q", a.Name(), a.Tag())
	}
}

func TestOnlyTheFirstPlusSeparates(t *testing.T) {
	a, err := Parse("inbox+a+b@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "inbox" || a.Tag() != "a+b" {
		t.Errorf("Name() = %q, Tag() = %q", a.Name(), a.Tag())
	}
}

func TestAnAddressWithoutATagIsItsOwnName(t *testing.T) {
	a, err := Parse("inbox@example.org")
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
		"@example.org",
		"inbox@",
		"inbox@example",
		"in box@example.org",
		`"in box"@example.org`,
		".inbox@example.org",
		"inbox.@example.org",
		"in..box@example.org",
		"inbox@[127.0.0.1]",
		"inbox@127.0.0.1",
		"inbox@-example.org",
		"inbox@example..org",
		"inbox@example_org.com",
		"пчёлка@example.org",
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded", s)
		}
	}
}

func TestAnAccountCannotBeRegisteredUnderATaggedAddress(t *testing.T) {
	if _, err := ParseAccount("inbox+shop@example.org"); err == nil {
		t.Error("ParseAccount accepted a +")
	}
	if _, err := ParseAccount("inbox@example.org"); err != nil {
		t.Error(err)
	}
}
