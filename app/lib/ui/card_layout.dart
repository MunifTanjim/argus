/// Shared horizontal geometry of session rows, cards and their section
/// headers, so marks, titles and body lines line up down the list.
library;

/// Left edge of a card's content, from the card's outer edge (a 3 px stripe
/// plus 12 px of padding, matched by every card style).
const double kCardInset = 15;

/// Space between session cards, in every style.
const double kCardGap = 8;

/// Width of the status mark column.
const double kLeadWidth = 14;

/// Space between the status mark and the title.
const double kLeadGap = 8;

/// Indent of a card's body lines, so they start under the title.
const double kLeadIndent = kLeadWidth + kLeadGap;
