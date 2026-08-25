Paint-over masks
================

Open <name>_paint.png, paint on top of the artwork, save, then re-run
cleanup.py.

    pure GREEN  #00FF00   force KEEP  (overrides any removal)
    pure RED    #FF0000   force DROP  (erases, even if the art is there)

Anything you don't paint is left to the automatic pass. Green beats red.

Use a hard pencil, 100% opacity, anti-aliasing OFF. Soft or semi-transparent
strokes blend with the sepia underneath and will be ignored.

Templates may be at a zoom factor (see scale in the filename note below); the
cleanup script works out the factor from the image size, so don't resize them.

Delete a template to go back to fully automatic for that sheet.
