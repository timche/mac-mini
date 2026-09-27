---
name: ui-writing
description: Write and review user-facing interface text to Apple Human Interface Guidelines quality on any platform (web app or website, macOS, Windows, Linux desktop, iOS, Android, CLI output shown to end users). Use this skill whenever you produce or change any string a person will read in a product: button and menu labels, dialog and alert titles, error and validation messages, empty states, toasts and notifications, tooltips, placeholders, settings labels and descriptions, onboarding copy, permission prompts, accessibility labels, page titles, i18n/localization string files, marketing-free product copy. Trigger even when the task is framed as code (a React component, a SwiftUI view, an Electron menu, a `strings.xml` or `en.json` file, a form schema) and the strings are incidental, and when reviewing a diff that touches such strings. Not for marketing taglines, documentation, commit messages, or code comments.
---

# UI writing

People do not read interfaces, they scan them while trying to do something else. Every string either moves them one step closer to that goal or gets in the way. Apple's Human Interface Guidelines are the best-documented standard for text that gets out of the way. This skill distills them into rules that apply on every platform. The one deliberate departure from Apple's own surface is capitalization: this skill uses sentence case everywhere unless the project states otherwise.

The rules below are the whole method for most tasks. Open `references/apple-hig.md` for the detail on a specific string type: it holds the HIG rules by component (alerts, buttons, menus, the macOS menu bar, notifications, permission requests, accounts, purchases, settings, onboarding, loading, feedback, undo, search, toolbars, labels, text fields) with Apple's own before/after examples. Read the relevant section before writing that component.

## Step 0: Know three things before writing

1. **Who reads it and what they are doing right then.** A payment error is read by someone anxious; a completed workout by someone pleased. Voice stays constant across the product, tone follows the moment.
2. **Where it renders.** The element type (button, alert title, body text, placeholder, notification) decides capitalization, punctuation and length. The device decides brevity: a phone, a watch and a TV all force shorter strings than a desktop window.
3. **What the project already does.** If the codebase has strings, a style guide or a CLAUDE.md that states a capitalization style, a term list or a perspective, that wins over the defaults here. Match it before introducing anything, and note inconsistencies rather than silently fixing unrelated ones.

## Core rules

**Clarity beats everything.** Choose the plain word people already use. Check each word; if the string works without it, cut it. Articles rarely earn their place in labels ("View settings", not "View the settings"). Read the result aloud: if it sounds like a robot or a lawyer, rewrite.

**Lead with the point.** The first two or three words carry the meaning, because that is all most people read and all a truncated notification shows. Put the most important information first on every screen.

**Actions are verbs.** Buttons, menu items and links that do something start with a verb naming the result: Send, Delete, Add to cart, Reply, Ignore. "Let's do it!" is worse than "Send". A label answers the question its title asks: for "Delete 3 photos?" the answers are "Delete" and "Cancel", never "Yes" and "No". "OK" is only for purely informational alerts, because on a confirmation it is unclear whether it means "do it" or "I understand".

**Links describe their destination.** "Learn more about sharing", not "Click here" or "Learn more" alone. Screen-reader users navigate by pulling the links out of context.

**Address people as "you"; avoid "we".** "The user" is distant. "We" is ambiguous and sounds like a corporation in an error ("We're having trouble loading this content" versus "Unable to load content"). Possessives are usually noise: "Favorites", not "Your Favorites". If you do use them, stay in one perspective across the product; never mix "My" and "Your".

**Errors: what happened, and what to do.** Show the message next to the problem, never blame, and give the fix. "Choose a password with at least 8 characters" beats "That password is too short"; "Use only letters for your name" beats "Don't use numbers or symbols"; "Invalid name" and "An error occurred" say nothing. The words *invalid*, *illegal*, *incorrect*, *failed* and *error* as a title, and the interjections *oops* and *uh-oh*, are all signs of a message that needs rewriting. A title like "Error 329347 occurred" is not a title. If many people will hit the same error, the fix is in the interaction, not the copy.

**Alert and dialog structure.** Be direct, neutral and approachable; do not mask severity. The title says what happened and, if useful, why, in one or two lines; a title that is a full sentence takes a period, a fragment takes no period. Body text only when it adds something, in complete sentences. Buttons are one or two words naming their result. Cancel is always titled "Cancel" and sits to the left of the confirming action. Never explain the buttons in the body. Do not raise an alert for information that is not actionable, for common undoable actions, or at launch. Errors go in an alert, not a notification or a toast.

**Destructive actions.** Name the object ("Delete 'Q3 budget.xlsx'?"), describe the consequence rather than asking "Are you sure?", give a safe exit, and make the destructive button say what it destroys ("Delete", "Erase", "Discard"). Never make the destructive button the default. Warn only when data loss is unexpected and irreversible; do not warn when loss is the obvious result of the action, such as emptying the trash.

**Ellipsis.** A label ends in the real ellipsis character (…) when the action needs more input before it completes, such as a menu item that opens a dialog ("Save as…", "Print…", "Settings…"). No ellipsis on Save, Close, Duplicate or anything that acts at once. A progress state also uses it: "Checkout" becomes "Checking out…".

**Multi-step flows.** Open with "Get started" or a specific first action, move with one consistent word ("Continue" or "Next", not both), and close with "Done".

**Toggles.** Prefer one item whose label reflects the current state ("Show map" becomes "Hide map"). If a state label could be read as a status rather than an action ("HDR on"), add the verb ("Turn HDR on").

**Empty states.** Say what the space is for and give one action that fills it, ideally as a button. Do not park important information there; it disappears once content exists.

**Settings.** Label by what the setting does when on; people infer the off state. Add a description only when the label is not enough. Link to a setting rather than describing where it lives. Call the area "Settings".

**Placeholders and field labels.** Every field has a visible label. Placeholder text is a format hint ("name@example.com") or a description ("Your name"), never the only label, because it vanishes as soon as someone types.

**Notifications and toasts.** Title in the first words with no period; body in full sentences. Do not include the app name, instructions to do something later, or anything private. Provide a neutral placeholder for hidden previews ("New comment", "Reminder"). Action buttons are short verbs; none of them should merely open the app. Never carry an error or the only path to a feature in something that auto-dismisses.

**Permission requests.** One active sentence that says what the app does with the access and why, with a period: "The app records during the night to detect snoring sounds." Not "Microphone access is needed for a better experience." Ask only when the feature needs it, not at launch. A pre-permission screen has one button, titled "Continue" or "Next", never "Allow", and no way to skip past it.

**Feedback and progress.** Confirm completion only for significant tasks; people expect success and need to hear about failure. Show something while content loads, and say how long if you know. When a command cannot run, say why.

**Accessibility labels.** Every image and icon-only control gets a label that says what it conveys or does ("Add contact", not "plus icon", not "image of"). Decorative images get no label. Page and window titles are unique and describe the content, not the app name. Headings describe their section.

**Inclusive, translatable language.** Plain words over jargon; define a technical term if it must appear. No idioms, no humor that depends on culture, no gendered pronouns where a plural or a rewrite works ("Subscribers can post recipes to your shared folder", not "let a subscriber post his or her recipes"). Numerals for counts ("3 messages"). Leave room: translations run 30 to 50 percent longer than English.

**Capitalization.** Sentence case for everything: buttons, menu items, titles, headings, labels, notification titles and body text. Capitalize the first word and proper nouns only; product and feature names keep their own capitalization. Never write a string in all caps; if a theme uppercases labels, that is styling, and the source string stays in sentence case. Title case is used only when the project states it (an existing catalog in title case, a style guide, or a CLAUDE.md), and then per element type, consistently.

**Consistency is a feature.** One term per concept across the product. Keep, or create, a short term list when a product has several names for the same thing and use it.

## Platform adaptation

The rules above apply unchanged on the web, on macOS, Windows and Linux, on iOS and Android, and in CLI output people read. Do not import conventions from other design systems; the point is one voice and one set of rules across the product. Only these things follow the host:

- **The interaction verb** describes the gesture the person makes: *tap* on a touch screen, *click* with a pointer, *choose* or *select* when the device is unknown or mixed (responsive web, cross-platform apps, help text), *press* for a physical key or button.
- **Key names** are the keys the keyboard has: "Command-N" on a Mac, "Ctrl+N" on Windows and Linux, both on the web.
- **Host-owned mechanisms.** Some HIG rules are stated through an Apple mechanism. Where the host has an equivalent, apply the rule through it: the permission explanation sentence goes in the system usage-description key on Apple platforms and in the app's own pre-permission screen elsewhere, the hidden-preview placeholder goes in whatever the host uses for a lock-screen version of a notification, screen-reader labels go in the host's accessibility tree, and Undo labels name the action in any Undo control. Where the host has no equivalent (the macOS menu bar's standard item names and Option-key alternates, action sheets, Face ID and Touch ID titles, the "passcode" rule, in-app purchase and refund wording, Family Sharing, Spotlight, Siri), the rule is Apple-only and does not carry over.
- **Brevity by device class** applies to any watch, phone or TV app, not only Apple's.

Everything else is the same everywhere: Cancel on the left and the confirming action on the right, the ellipsis rule, "Settings" for the configuration area, "Trash" for discarded items, "Quit" for leaving a desktop app, "Sign in" and "Sign out", "Delete" for destroying and "Remove" for taking out of a list.

**Cross-platform products** (Electron apps, responsive web apps, shared string catalogs) keep one catalog and vary only the interaction verb and key names per platform. Never case-transform strings in code; the string in the catalog is the string. A native macOS menu bar uses Apple's standard item names exactly; the Apple reference lists them under "The menu bar".

## Working method

1. Identify every string in scope, including labels that only screen readers hear, and the element type of each.
2. Establish the platform and the existing conventions in the codebase (case, term list, perspective). Match them.
3. Write each string by the rules above; for a component with its own section in the Apple reference, read that section first.
4. Review the set together: same verb for the same action everywhere, same case per element type, no "we", no button explained in body text, every error with a fix, every link that says where it goes.
5. When reporting, quote each changed string as before and after in a fenced block and say which rule drove the change. If a string is right by these rules but breaks the product's own existing convention, say so and leave the convention decision to the user.

## Quick reference of rewrites

```
Let's do it!                          -> Send
Click here                            -> Learn more about sharing
Are you sure?                         -> Delete "Q3 budget.xlsx"?   [Cancel] [Delete]
Yes / No                              -> Keep / Discard
OK  (on a consequential alert)        -> Erase
Error 329347 occurred                 -> Can't save document
Invalid name                          -> Use only letters for your name
That password is too short            -> Choose a password with at least 8 characters
Oops! Something went wrong            -> Unable to load messages. Check your connection.
We're having trouble loading content  -> Unable to load content
Your Favorites                        -> Favorites
Turn on microphone access.            -> The app records audio to transcribe your meetings.
Sign In / Log In                      -> Sign in
Show map / Hide map (two items)       -> one item that toggles between Show map and Hide map
HDR on / HDR off (ambiguous)          -> Turn HDR on / Turn HDR off
Save as  (opens a dialog)             -> Save as…
plus icon  (accessibility label)      -> Add contact
Tap Continue  (on a desktop web app)  -> Click Continue
Add to Cart                           -> Add to cart
```
