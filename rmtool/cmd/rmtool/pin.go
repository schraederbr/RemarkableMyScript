package main

import (
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/akeil/rmtool"
)

func doPin(s settings, match string, pinned bool) error {
	repo, err := setupRepo(s)
	if err != nil {
		return err
	}

	items, err := repo.List()
	if err != nil {
		return err
	}

	root := rmtool.BuildTree(items)
	matches := rmtool.MatchName(match)

	var group errgroup.Group
	root.Walk(func(n *rmtool.Node) error {
		if matches(n) {
			group.Go(func() error {
				n.SetPinned(pinned)
				err := repo.Update(n)
				if err != nil {
					fmt.Printf("%v Failed to change bookmark for %q: %v", crossmark, n.Name(), err)
				} else {
					if pinned {
						fmt.Printf("%v Bookmarked %q\n", checkmark, n.Name())
					} else {
						fmt.Printf("%v Removed bookmark for %q\n", checkmark, n.Name())
					}
				}
				return err
			})
		}
		return nil
	})

	return group.Wait()
}
