# Apple Human Interface Guidelines: writing rules

Condensed from developer.apple.com/design/human-interface-guidelines (fetched 2026-09-10). Quoted text is Apple's. Lines marked `[inferred]` are the digester's reading, not Apple's words.

This reference is the source of truth on every platform. Where a rule is stated through an Apple mechanism (a system permission alert, VoiceOver, the macOS menu bar, Dynamic Type), SKILL.md's "Platform adaptation" section says whether the rule carries over through the host's equivalent or is Apple-only. One deliberate override: wherever Apple says "title-style capitalization", the skill uses sentence case unless the project states otherwise. Every other rule stands as written.

Table of contents

- Part 1: Foundations (Writing, Inclusion, Accessibility, VoiceOver pages)
- Part 2: Rules by component (alerts, buttons, menus, menu bar, notifications, permissions, accounts, purchases, settings, onboarding, loading, feedback, undo, search, toolbars, labels, text fields, keyboards)
- Part 3: Cross-cutting summary (capitalization, punctuation, ellipsis, word lists)

---

# Part 1: Foundations

## 1. Voice and tone (Writing → Getting started)

- **Determine your app's voice.** "Think about who you're talking to, so you can figure out the type of vocabulary you'll use." What words are familiar to these people? How do you want them to feel? "The words for a banking app might convey trust and stability, for example, while the words in a game might convey excitement and fun."
- **Keep a term list.** "Create a list of common terms, and reference that list to keep your language consistent. Consistent language, along with a voice that reflects your app's values, helps everything feel more cohesive."
- **Match your tone to the context.** Voice is fixed; tone varies with the situation. "Consider what people are doing while they're using your app — both in the physical world and within the app itself. Are they exercising and reached a goal? Or are they trying to make a payment and received an error? Situational factors affect both what you say and how you display the text on the screen."
- Apple's illustrated pair (Apple Watch): in the first example "the tone is straightforward and direct, reflecting the seriousness of the situation"; in the second "the tone is light and congratulatory." (The screenshots themselves are images; their strings aren't in the page text.)
- **Consider the tone of your copy from different perspectives** (Inclusion). "The style of your writing communicates almost as much as the words you use." Example of an unintended message: "an academic tone can make an app or game seem like it welcomes only high levels of education." Be "clear, direct, and respectful."
- **Consider carefully before including humor** (Inclusion). "Humor is highly subjective and — similar to colloquial expressions — difficult to translate from one culture to another. Including humor in your experience risks confusing people who don't understand it, irritating people who tire of repeatedly encountering it, and insulting people who interpret it differently."

## 2. Clarity and brevity

- **Be clear.** "Choose words that are easily understood and convey the right thing. Check each word to be sure it needs to be there. If you can use fewer words, do so. When in doubt, read your writing out loud."
- **Write for everyone.** "For your app to be useful for as many people as possible, it needs to speak to as many people as possible. Choose simple, plain language and write with accessibility and localization in mind, avoiding jargon and gendered terminology."
- **Consider each screen's purpose.** "Pay attention to the order of elements on a screen, and put the most important information first. Format your text to make it easy to read. If you're trying to convey more than one idea, consider breaking up the text onto multiple screens, and think about the flow of information across those screens."
- Brevity is device-driven, not universal: iPhone and Apple Watch "require brevity" because of small screens; TVs "also require brevity, as the text must be large for people to see it from a distance."

## 3. Jargon, colloquialisms, terminology

- **Avoid using specialized or technical terms without defining them** (Inclusion). "Using specialized or technical terms can make your writing more succinct, but doing so excludes people who don't know what the terms mean. If you must use such terms, be sure to define them first and make the definitions easy for people to look up. Even when people know the definition of a specialized or technical term in a sentence, the sentence is easier to read — and translate — when it uses plain language instead."
- **Replace colloquial expressions with plain language** (Inclusion). "Colloquial expressions are often culture-specific and can be difficult to translate. Worse, some colloquial phrases have exclusionary meanings you might not know. For example, the phrases **peanut gallery** and **grandfathered in** both arose from oppressive contexts and continue to exclude people. Even when a colloquial phrase doesn't have an exclusionary meaning, it can still exclude everyone who doesn't understand it."
- **Use the right term per device.** "Make sure you describe gestures correctly on each device — for example, not saying 'click' for a touch device like iPhone or iPad where you mean 'tap.'"

## 4. Person and pronouns ("you", "we", possessives)

- **Address people as "you."** (Inclusion) "It typically works well to use **you** and **your** to address people directly. Referring to people indirectly as **the user** or **the player** can make your experience feel distant and unwelcoming."
- **Reserve "we"/"our" for your software or company.** (Inclusion) "otherwise, these terms can suggest a personal relationship with people that might be interpreted as insulting or condescending."
- **Use possessive pronouns sparingly.** (Writing) "Possessive pronouns like **my** and **your** are often unnecessary to establish context. For example, 'Favorites' conveys the same message as 'Your Favorites,' and is more succinct. If you do use possessive pronouns, use them consistently throughout your app, and try not to switch perspectives."
- **Avoid "we" altogether** (Writing). "because it may be unclear who the 'we' in question refers to. This is particularly problematic in error messages like 'We're having trouble loading this content.' Something like 'Unable to load content' is much clearer."
  - Before/after: ~~"We're having trouble loading this content."~~ → "Unable to load content"

## 5. Capitalization: sentence case vs title case

Apple's stated rule (Writing):

> **Adopt capitalization rules that align with your app's style, then apply them consistently.** While certain components, like buttons, have specific guidelines, how you format text reflects your app's voice. **Title case is generally considered formal, while sentence case is more casual.** Choose a style for each UI element type and use it consistently throughout your app — for example, title case for all alerts or sentence case for all headlines.

- So: the HIG does **not** mandate one case globally. It mandates *per-element-type consistency*, with component pages overriding.
- The component override it names is buttons; the Buttons page says to write the label "Using title-style capitalization" (linking to the Apple Style Guide, which holds the actual title-case word rules — those rules are in the Style Guide, not in any of the three requested HIG pages).
- [inferred] Nothing on these pages gives rules for punctuation, numbers, or heading capitalization beyond the above; that material lives in the Apple Style Guide (help.apple.com/applestyleguide), which the HIG links to repeatedly but which was outside the requested fetch.

## 6. Buttons, links, and action labels

- **Be action oriented.** "Active voice and clear labels help people navigate through your app from one step to the next, or from one screen to another. When labeling buttons and links, **it's almost always best to use a verb.** Prioritize clarity and avoid the temptation to be too cute or clever with your labels."
  - Before/after: "just saying 'Send' often works better than 'Let's do it!'"
  - Links: "avoid using 'Click here' in favor of more descriptive words or phrases, such as 'Learn more about UX Writing.' **This is especially important for people using screen readers to access your app.**"
- Buttons page: "write a few words that succinctly describe what the button does. Using title-style capitalization, consider starting the label with a verb to help convey the button's action — for example, a button that lets people add items to their shopping cart might use the label 'Add to Cart.'"
- Buttons page, in-progress labels: "the label 'Checkout' could change to 'Checking out…' while the activity indicator is visible."

## 7. Consistency and multi-step flows

- **Build language patterns.** "Consistency builds familiarity, helping your app feel cohesive, intuitive, and thoughtfully designed. It also makes writing for your app easier, as you can return to these patterns again and again."
- **Give clear guidance and use consistent language throughout processes with multiple steps.** "Begin with language like 'Get Started' to indicate you're starting a flow. You can use the button label to hint at the next step, or use terms like 'Continue' or 'Next,' but be consistent with what you choose. Make it clear when a flow is complete by using language like 'Done.'"

## 8. Error messages

> **Write clear error messages.** It's always best to help people avoid errors. When an error message is necessary, display it as close to the problem as possible, avoid blame, and be clear about what someone can do to fix it. For example, "That password is too short" isn't as helpful as "Choose a password with at least 8 characters." Remember that errors can be frustrating. Interjections like "oops!" or "uh-oh" are typically unnecessary and can sound insincere. If you find that language alone can't address an error that's likely to affect many people, use that as an opportunity to rethink the interaction.

- Before/after: ~~"That password is too short"~~ → "Choose a password with at least 8 characters."
- Banned interjections, named: "oops!", "uh-oh".

## 9. Text fields and form errors

> **Show hints in text fields.** If your app allows people to enter their own text, like account or contact information, label all fields clearly, and use hint or placeholder text so people know how to format the information. You can give an example in hint text, like "name@example.com," or describe the information, such as "Your name." Show errors right next to the field, and instruct people how to enter the information correctly, rather than scolding them for not following the rules. "Use only letters for your name" is better than "Don't use numbers or symbols." Avoid robotic error messages with no helpful information, like "Invalid name."

- Before/after: ~~"Don't use numbers or symbols."~~ → "Use only letters for your name"; and ~~"Invalid name."~~ (no useful information).

## 10. Empty states

> **Provide clear next steps on any blank screens.** An empty state, like a completed to-do list or bookmarks folder with nothing in it, can provide a good opportunity to make people feel welcome and educate them about your app. Empty states can also showcase your app's voice, but make sure that the content is useful and fits the context. An empty screen can be daunting if it isn't obvious what to do next, so guide people on actions they can take, and give them a button or link to do so if possible. Remember that empty states are usually temporary, so don't show crucial information that could then disappear.

## 11. Settings labels

> **Keep settings labels clear and simple.** Help people easily find the settings they need by labeling them as practically as possible. If the setting label isn't enough, add an explanation. **Describe what it does when turned on, and people can infer the opposite.** In the Handwashing Timer setting for Apple Watch, for example, the description explains that a timer can start when you're washing your hands. It isn't necessary to tell you that a timer won't start when this setting is off.

- "If you need to direct someone to a setting, provide a direct link or button, rather than trying to describe its location."

## 12. Choosing the delivery method

> **Choose the right delivery method.** There are many ways to get people's attention, whether or not they are actively using your app. When there's something you want to communicate, consider the urgency and importance of the message. Think about the context in which someone might see the message, whether it requires immediate action, and how much supporting information someone might need. Choose the correct delivery method, and use a tone appropriate for the situation. (See Notifications, Alerts, Action sheets.)

## 13. Writing across devices

- "People may use your app on several types of devices. While your language needs to be consistent across them, think about where it would be helpful to adjust your text to make it suitable for different devices."
- "Where and how people use a device, its screen size, and its location all affect how you write for your app. iPhone and Apple Watch, for example, offer opportunities for personalization, but their small screens require brevity. TVs, on the other hand, are often in common living spaces, and several people are likely to see anything on the screen, so consider who you're addressing."

## 14. Inclusive language

- Gender: "avoid unnecessary references to specific genders."
  - Before/after (verbatim): ~~"You can let a subscriber post his or her recipes to your shared folder"~~ → "Subscribers can post recipes to your shared folder." Apple's rationale: it uses "the gender-neutral noun 'subscribers'" and "avoids the unnecessary singular pronouns 'his' and 'her,' helping the sentence remain inclusive when it's localized for languages that use gendered pronouns."
  - Avatars, emoji, glyphs, game characters: "prefer giving people the tools they need to customize such items as they choose." For a generic person, "use a nongendered human image to reinforce the message that **generic person** means **human**, not **man** or **woman**" (SF Symbols figure / person symbols).
  - If you genuinely need gender ("such as for health or legal reasons"), offer "**nonbinary**, **self-identify**, and **decline to state**," and consider letting people specify their pronouns.
- Disability language: "avoid language that uses a disability to express a negative quality." "**Take a people-first approach when writing about people with disabilities.** For example, you could describe an individual's accomplishments and goals before mentioning a disability they may have. If you're writing about a specific person or community, find out how they self-identify."
- Assumptions and stereotypes — Apple's verbatim security-question example:
  - Exclusionary set: "What was your favorite subject in college?" / "What was the make of your first car?" / "How did you feel when you first saw a rainbow?" — "all are based on experiences that not everyone has."
  - Universal replacements: "What's your favorite activity?" / "What was the name of your first friend?" / "What quality describes you best?"
  - Also named: an app that assumes **family** means "a woman, a man, and their biological children" excludes everyone whose family differs.
- Representation: "avoid stereotypical representations, such as showing only male doctors, female nurses, or heroes and villains that may perpetuate real-world racial or gender stereotypes." Review settings and objects too: "showing high levels of affluence … can be unwelcoming and make an experience seem out of touch."
- Framing: "avoid framing the work as merely a search for content that might give offense … an inoffensive app or game isn't necessarily an inclusive one."
- Perspectives Apple lists as shared human characteristics to design against: age; gender and gender identity; race and ethnicity; sexuality; physical attributes; cognitive attributes; permanent, temporary, and situational disabilities; language and culture; religion; education; political or philosophical opinions; social and economic context.
- Disability is a spectrum, and includes **temporary disabilities** ("short-term hearing loss due to an infection") and **situational disabilities** ("being unable to hear while on a noisy train").

## 15. Localization-driven writing rules

- "using plain language, avoiding unnecessary gender references, representing a variety of people, and avoiding stereotypes and culture-specific content, can put you in a good position to create versions of your software localized into more languages."
- Color carries culture-specific meaning: "In some places, for example, white is associated with death or grief, whereas in other places, it's associated with purity or peace. If you use color as a way to communicate, make sure your color choices communicate the same thing in each version of your software."

## 16. Accessibility labels and VoiceOver text (VoiceOver page)

- **Provide alternative labels for all key interface elements.** "VoiceOver uses alternative labels (which aren't visible onscreen) to audibly describe your app's interface. System-provided controls have generic labels by default, but you should provide more descriptive labels that convey your app's functionality. Add labels to any custom elements your app defines. Be sure to keep your descriptions up-to-date as your app's interface and content change."
- **Describe meaningful images.** "Because VoiceOver helps people understand the interface surrounding images too, such as nearby captions, **describe only the information the image itself conveys.**"
- **Exclude purely decorative images from VoiceOver.** "It's unnecessary to describe images that are decorative and don't convey useful or actionable information. Excluding these images shows respect for people's time and reduces cognitive load."
- **Charts and infographics:** "Provide a concise description of each infographic that explains what it conveys," and expose its interactions to VoiceOver too.
- **Titles and headings:** "The title is the first information someone receives from an assistive technology when arriving on a page or screen in your app. Offer **unique titles** that succinctly describe each page's content and purpose. Likewise, use accurate section headings that help people build a mental model of each page's information hierarchy."
- **Describe relationships that are only visual.** "Examine your app for places where relationships among elements are visual only. Then, describe these relationships to VoiceOver." Reading order follows the active language and locale (US English: top-to-bottom, left-to-right); grouping an image with its caption makes VoiceOver read them together instead of all images then all captions.
- **Announce content or layout changes** so people can update their mental map.
- **Support the VoiceOver rotor** (navigate by headings, links, content types).
- From the Accessibility page: label interface elements appropriately so **Voice Control** works ("people can interact with their devices entirely by speaking commands"), and verify labels for AssistiveTouch, Full Keyboard Access, Pointer Control, and Switch Control.

## 17. Text as an accessibility surface (Accessibility page, text-related only)

- **Support larger text sizes** — "give people the option to enlarge text by at least 200 percent (or 140 percent in watchOS apps)"; adopt Dynamic Type.
- **Use recommended defaults for custom type sizes**; thin custom weights should go "larger than the recommended sizes to increase legibility."
- **Contrast:** aim for WCAG Level AA ratios (the Accessibility Inspector uses those values); APCA is named as the other common standard.
- **Never carry dialogue or crucial information in audio alone.** Provide text-based equivalents and let people customize their presentation:
  - **Captions** — "the textual equivalent of audible information in video or audio-only content … great for scenarios like game cutscenes and video clips where text synchronizes live with the media."
  - **Subtitles** — "allow people to read live onscreen dialogue in their preferred language … great for TV shows and movies."
  - **Transcripts** — "a complete textual description of a video, covering both audible and visual information … great for longer-form media like podcasts and audiobooks."
- **Cognitive load:** keep actions simple and intuitive; "minimize use of time-boxed interface elements" that auto-dismiss, since they penalize people who need longer to process information; for Assistive Access, "break up multistep workflows so people can focus on a single interaction per screen" and "always ask for confirmation twice whenever people perform an action that's difficult to recover from, such a [sic] deleting a file."

## 18. Change log (Writing page)

| Date | Change |
| --- | --- |
| December 16, 2025 | "Clarified guidance on language patterns, and added guidance for possessive pronouns." |
| February 27, 2023 | New page. |

## Not covered by these pages

Apple states no rules here for punctuation, numerals, dates/times, units, or title-case word-by-word mechanics. The Writing, Inclusion, and Buttons pages all defer those to the **Apple Style Guide** (https://help.apple.com/applestyleguide/), which was outside this fetch.

---

# Part 2: Rules by component

## Alerts

**Tone**
- "In all alert copy, be direct, and use a neutral, approachable tone. Alerts often describe problems and serious situations, so avoid being oblique or accusatory, or masking the severity of the issue."

**Title**
- "Write a title that clearly and succinctly describes the situation. You need to help people quickly understand the situation, so be complete and specific, without being verbose. As much as possible, describe what happened, the context in which it happened, and why. Avoid writing a title that doesn't convey useful information — like "Error" or "Error 329347 occurred" — but also avoid overly long titles that wrap to more than two lines. If the title is a complete sentence, use sentence-style capitalization and appropriate ending punctuation. If the title is a sentence fragment, use title-style capitalization, and don't add ending punctuation."
- (iOS/iPadOS) "keeping alert titles short and including a brief message only when necessary" to avoid a scrolling alert.

**Message / informative text**
- "Include informative text only if it adds value. If you need to add an informative message, keep it as short as possible, using complete sentences, sentence-style capitalization, and appropriate punctuation."
- "Avoid explaining alert buttons. If your alert text and button titles are clear, you don't need to explain what the buttons do. In rare cases where you need to provide guidance on choosing a button, use a term like *choose* to account for people's current device and interaction method, and refer to a button using its exact title without quotes."

**Button titles**
- "Create succinct, logical button titles. Aim for a one- or two-word title that describes the result of selecting the button. Prefer verbs and verb phrases that relate directly to the alert text — for example, "View All," "Reply," or "Ignore." In informational alerts only, you can use "OK" for acceptance, avoiding "Yes" and "No." Always use "Cancel" to title a button that cancels the alert's action. As with all button titles, use title-style capitalization and no ending punctuation."
- "Avoid using OK as the default button title unless the alert is purely informational. The meaning of "OK" can be unclear even in alerts that ask people to confirm that they want to do something. For example, does "OK" mean "OK, I want to complete the action" or "OK, I now understand the negative results my action would have caused"? A specific button title like "Erase," "Convert," "Clear," or "Delete" helps people understand the action they're taking."
- "If there's a destructive action, include a Cancel button to give people a clear, safe way to avoid the action. Always use the title "Cancel" for a button that cancels an alert's action. Note that you don't want to make a Cancel button the default button. If you want to encourage people to read an alert and not just automatically press Return to dismiss it, avoid making any button the default button. Similarly, if you must display an alert with a single button that's also the default, use a Done button, not a Cancel button."
- "Use the destructive style to identify a button that performs a destructive action people didn't deliberately choose." Apple's own example: an Empty Trash alert does **not** use the destructive style on the Empty Trash button, because that button performs the person's original intent.

**When not to write one at all**
- "Avoid using an alert merely to provide information. People don't appreciate an interruption from an alert that's informative, but not actionable."
- "Avoid displaying alerts for common, undoable actions, even when they're destructive."
- "Avoid showing an alert when your app starts." Alternative given: "show cached or placeholder data and a nonintrusive label that describes the problem."
- macOS: use the caution symbol sparingly; "Don't use the symbol for tasks whose only purpose is to overwrite or remove data, such as a save or empty trash."

---

## Action sheets

- "Aim to keep titles short enough to display on a single line. A long title is difficult to read quickly and might get truncated or require people to scroll."
- "Provide a message only if necessary. In general, the title — combined with the context of the current action — provides enough information to help people understand their choices."
- "If necessary, provide a Cancel button that lets people reject an action that might destroy data." Place it at the bottom (upper-left in watchOS).
- Button styles carry meaning: **Default** — "The button has no special meaning." **Destructive** — "The button destroys user data or performs a destructive action in the app." **Cancel** — "The button dismisses the view without taking any action."
- watchOS: "Avoid displaying more than four buttons in an action sheet, including the Cancel button."
- Use an action sheet, not an alert, for "choices related to an intentional action."

---

## Buttons

**Text content**
- "Consider using text when a short label communicates more clearly than an icon. To use text, write a few words that succinctly describe what the button does. Using title-style capitalization, consider starting the label with a verb to help convey the button's action — for example, a button that lets people add items to their shopping cart might use the label "Add to Cart.""
- macOS tooltips: "A tooltip displays a brief phrase that explains what a button does."

**Ellipsis rule (macOS push buttons)**
- "Append a trailing ellipsis to the title when a push button opens another window, view, or app. Throughout the system, an ellipsis in a control title signals that people can provide additional input. For example, the Edit buttons in the AutoFill pane of Safari Settings include ellipses because they open other views that let people modify autofill values."

**Progress wording inside a button (iOS/iPadOS)**
- "you can also configure the button to display a different label alongside the activity indicator. For example, the label "Checkout" could change to "Checking out…" while the activity indicator is visible."

**Roles**
- Primary = "the button people are most likely to choose"; Cancel = "The button cancels the current action"; Destructive = "The button performs an action that can result in data destruction."
- "Don't assign the primary role to a button that performs a destructive action, even if that action is the most likely choice."

**Labels you should not write**
- Square buttons: "Avoid using labels to introduce square buttons. Because square buttons are closely connected with a specific view, their purpose is generally clear without the need for descriptive text."
- Help buttons: "Avoid displaying text that introduces a help button. People know what a help button does, so they don't need additional descriptive text."
- visionOS: "buttons that contain text don't need to display a tooltip because the button's descriptive label communicates what it does."

---

## Menus (menu items, all menu types)

**Wording**
- "For each menu item, write a label that clearly and succinctly describes it. In general, label a menu item that initiates an action using a verb or verb phrase that describes the action, such as View, Close, or Select. … As with all the copy you write, let your app's or game's communication style guide the tone of the menu-item labels you create."
- "To be consistent with platform experiences, use title-style capitalization. … which capitalizes every word except articles, coordinating conjunctions, and short prepositions, and capitalizes the last word in the label, regardless of the part of speech."
- "Remove articles like *a*, *an*, and *the* from menu-item labels to save space. In English, articles always lengthen labels, but rarely enhance understanding. For example, changing a menu-item label from  View Settings to View the Settings doesn't provide additional clarification." *(Apple's sentence reads exactly this way on the page — a ✗/✓ comparison whose markers are images. [inferred] the intended reading is: prefer "View Settings" over "View the Settings".)*

**Ellipsis**
- "Append an ellipsis to a menu item's label when the action requires more information before it can complete. The ellipsis character (…) signals that people need to input information or make additional choices, typically within another view."

**Unavailable items**
- "Show people when a menu item is unavailable. An unavailable menu item often appears dimmed and doesn't respond to interactions. If all of a menu's items are unavailable, the menu itself needs to remain available…"

**Toggled items (show/hide, on/off)**
- "Consider using a changeable label that describes an item's current state. For example, instead of listing two menu items like Show Map and Hide Map, you could include one menu item whose label changes from Show Map to Hide Map, depending on whether the map is visible."
- "Include a verb if a changeable label isn't clear enough. For example, people might not know whether the changeable labels HDR On and HDR Off describe actions or states. If you needed to clarify that these items represent actions, you could add verbs to the labels, like Turn HDR On and Turn HDR Off."
- "If necessary, display both menu items instead of one toggled item. … a game could list both Take Account Online and Take Account Offline items…"
- "Consider offering a menu item that makes it easy to remove multiple toggled attributes. For example, … a menu item — such as Plain — that removes all applied formatting attributes at one time."

**Grouping and submenu naming**
- "Consider grouping logically related items. For example, grouping editing commands like Copy, Cut, and Paste…"
- "You might consider creating a submenu when a term appears in more than two menu items in the same group. For example, instead of offering separate menu items for Sort by Date, Sort by Score, and Sort by Time, a game could present a menu item that uses a submenu to list the sorting options Date, Score, and Time. It generally works well to use the repeated term — in this case, Sort by — in the menu item's label to help people predict the contents of the submenu."

---

## The menu bar (macOS / iPadOS standard names)

**Menu titles**
- Order, when present: "YourAppName (you supply a short version of your app's name for this menu's title)", File, Edit, Format, View, app-specific menus, Window, Help.
- "Prefer short, one-word menu titles. … If you need to use more than one word in the menu title, use title-style capitalization."
- "Always show the same set of menu items. … If a menu bar item isn't actionable, disable the action instead of hiding it from the menu."
- App-specific menus: "Aim to list app-specific menus in order from most to least general or commonly used."

**App menu** — standard item names and wording rules
- `About YourAppName` — "Prefer a short name of 16 characters or fewer. Don't include a version number."
- `Settings…` — "Use only for app-level settings. If you also offer document-specific settings, put them in the File menu." (Note the ellipsis.)
- Optional app-specific items — "List custom app-configuration items after the Settings item and within the same group."
- `Services` (macOS only), `Hide YourAppName` (macOS only, "Use the same short app name you supply for the About item"), `Hide Others`, `Show All`.
- `Quit YourAppName` — "Pressing Option changes Quit YourAppName to Quit and Keep Windows." Same short app name.
- "Display the About menu item first."

**File menu** — standard names
- `New Item` — "For Item, use a term that names the type of item your app creates. For example, Calendar uses Event and Calendar."
- `Open` — "If people need to select an item in a separate interface, an ellipsis follows the command to indicate that more input is required."
- `Open Recent` — includes a `Clear Menu` item; "List document and filenames that people recognize in the submenu; don't display file paths."
- `Close` — "Pressing Option changes Close to Close All. For a tab-based window, Close Tab replaces Close." Consider adding `Close Window`.
- `Close Tab` — "Pressing Option changes Close Tab to Close Other Tabs."
- `Close File`, `Save`, `Save All`.
- `Duplicate` — "Pressing Option changes Duplicate to Save As. Prefer Duplicate to menu items like Save As, Export, Copy To, and Save To because these items don't clarify the relationship between the original file and the new one."
- `Rename…`, `Move To…`, `Export As…` ("Reserve the Export As item for when you need to let people export content in a format your app doesn't typically handle."), `Revert To`, `Page Setup…`, `Print…`.
- [inferred] Apple's own list shows the ellipsis on exactly: Settings…, Rename…, Move To…, Export As…, Page Setup…, Print… — and *not* on Save, Duplicate, Close, Open.

**Edit menu** — standard names
- `Undo` — "Clarify the target of the undo. For example, if people just selected a menu item, you can append the item's title, such as Undo Paste and Match Style. For a text entry operation, you might append the word Typing to give Undo Typing."
- `Redo` — same rule: "Redo Paste and Match Style", "Redo Typing".
- `Cut`, `Copy`, `Paste`, `Paste and Match Style`.
- `Delete` — "Provide a Delete menu item instead of an Erase or Clear menu item. Choosing Delete is the equivalent of pressing the Delete key, so it's important for the naming to be consistent."
- `Select All`; `Find` submenu: "Find, Find and Replace, Find Next, Find Previous, Use Selection for Find, and Jump to Selection."
- `Spelling and Grammar` submenu: "Show Spelling and Grammar, Check Document Now, Check Spelling While Typing, Check Grammar With Spelling, and Correct Spelling Automatically."
- `Substitutions` submenu: "Show Substitutions, Smart Copy/Paste, Smart Quotes, Smart Dashes, Smart Links, Data Detectors, and Text Replacement."
- `Transformations` submenu: "Make Uppercase, Make Lowercase, and Capitalize."
- `Speech` submenu: "Start Speaking and Stop Speaking."
- `Start Dictation`, `Emoji & Symbols` (system-added).

**Format menu** — `Font` submenu: "Show Fonts, Bold, Italic, Underline, Bigger, Smaller, Show Colors, Copy Style, and Paste Style." `Text` submenu: "Align Left, Align Center, Justify, Align Right, Writing Direction, Show Ruler, Copy Ruler, and Paste Ruler."

**View menu** — show/hide wording
- "Ensure that each show/hide item title reflects the current state of the corresponding view. For example, when the toolbar is hidden, provide a Show Toolbar menu item; when the toolbar is visible, provide a Hide Toolbar menu item."
- Standard items: `Show/Hide Tab Bar`, `Show All Tabs/Exit Tab Overview`, `Show/Hide Toolbar`, `Customize Toolbar`, `Show/Hide Sidebar`, `Enter/Exit Full Screen`.

**Window menu** — `Minimize` ("Pressing the Option key changes this item to Minimize All"), `Zoom` ("…to Zoom All"; "Avoid using Zoom to enter or exit full-screen mode"), `Show Previous Tab`, `Show Next Tab`, `Move Tab to New Window`, `Merge All Windows`, `Enter/Exit Full Screen`, `Bring All to Front` ("Pressing the Option key changes this item to Arrange in Front"), then open window names — "List the currently open windows in alphabetical order for easy scanning. Avoid listing panels or other modal views."

**Help menu** — `Send YourAppName Feedback to Apple`, `YourAppName Help`, then additional items: "Use a separator between your primary help documentation and additional items, which might include registration information or release notes. Keep the total the number of items you list in the Help menu small…"

**iPadOS**: "Reserve the YourAppName > Settings menu item for opening your app's page in iPadOS Settings. If your app includes its own internal preferences area, link to it with a separate menu item beneath Settings in the same group."

---

## Keyboards (text guidance only)

- "Define custom keyboard shortcuts for only the most frequently used app-specific commands. … defining too many new shortcuts can make your app seem difficult to learn."
- "In general, don't repurpose standard keyboard shortcuts for custom actions."
- "List modifier keys in the correct order. If you use more than one modifier key in a custom shortcut, always list them in this order: Control, Option, Shift, Command."
- "Avoid adding Shift to a shortcut that uses the upper character of a two-character key. … For example, the keyboard shortcut for Hide Status Bar is Command-Slash, whereas the keyboard shortcut for Help is Command-Question mark, not Shift-Command-Slash."
- "Let the system localize and mirror your keyboard shortcuts as needed."
- visionOS: "Write descriptive shortcut titles. Because the shortcut interface displays a flat list of all items in each category, submenu titles aren't available to provide context for their child items. Make sure each shortcut title is descriptive enough to convey its action without the additional context a submenu title might provide."
- Naming convention visible throughout Apple's own table: shortcuts are written "Command-A", "Option-Command-C", "Option-Shift-Command-V", "Command-Semicolon", "Option-Shift-Right arrow" — modifier names spelled out, joined by hyphens, key named in full.

---

## Toolbars

- "Provide a useful title for each window. … If titling a toolbar seems redundant, you can leave the title area empty."
- "Don't title windows with your app name. Your app's name doesn't provide useful information about your content hierarchy or any window or area in your app, so it doesn't work well as a title."
- "Write a concise title. Aim for a word or short phrase that distills the purpose of the window or view, and keep the title under 15 characters long so you leave enough room for other controls."
- "Use the standard Back and Close buttons. … Prefer the standard symbols for each, and don't use a text label that says Back or Close."
- "Prefer simple, recognizable symbols for items instead of text, except for actions like edit that aren't well-represented by symbols."
- "Use the `.prominent` style for key actions such as Done or Submit. … Only specify one primary action, and put it on the trailing side of the toolbar."
- "Group navigation controls and critical actions like Done, Close, or Save in dedicated, familiar, and visually distinct sections."
- "Keep actions with text labels separate. Placing an action with a text label next to an action with a symbol can create the illusion of a single action with a combined text and symbol… If your toolbar includes multiple text-labeled buttons, the text of those buttons may appear to run together."
- Leading-edge document menu contains "standard and app-specific commands that affect the document as a whole, such as Duplicate, Rename, Move, and Export."
- macOS: "Make every toolbar item available as a command in the menu bar."

---

## Labels

- "Within a button, a label generally conveys what the button does, such as Edit, Cancel, or Send."
- "Within many lists, a label can describe each item, often accompanied by a symbol or an image."
- "Within a view, a label might provide additional context by introducing a control or describing a common action or task that people can perform in the view."
- "Use a label to display a small amount of text that people don't need to edit."
- "Use system-provided label colors to communicate relative importance": Label = "Primary information"; Secondary label = "A subheading or supplemental text"; Tertiary label = "Text that describes an unavailable item or behavior"; Quaternary label = "Watermark text".
- "Make useful label text selectable. If a label contains useful information — like an error message, a location, or an IP address — consider letting people select and copy it for pasting elsewhere."

---

## Text fields (placeholder text)

- "Show a hint in a text field to help communicate its purpose. A text field can contain placeholder text — such as "Email" or "Password" — when there's no other text in the field. Because placeholder text disappears when people start typing, it can also be useful to include a separate label describing the field to remind people of its purpose."
- "Use secure text fields to hide private data. Always use a secure text field when your app asks for sensitive data, such as a password."
- "Validate fields when it makes sense. For example, if the only legitimate value for a field is a string of digits, your app needs to alert people if they've entered characters other than digits. The appropriate time to check the data depends on the context: when entering an email address, it's best to validate when people switch to another field; when creating a user name or password, validation needs to happen before people switch to another field."
- "Adjust line breaks according to the needs of the field. … you can set up a text field to wrap text to a new line at the character or word level, or to truncate (indicated by an ellipsis) at the beginning, middle, or end."
- "Consider using an expansion tooltip to show the full version of clipped or truncated text."
- "Don't assume the actual presentation of data, however, as formatting can vary significantly based on people's locale."

---

## Notifications

**Title**
- "Create a short title if it provides context for the notification content. Prefer brief titles that people can read at a glance… When possible, take advantage of the prominent notification title area to provide useful information, like a headline, event name, or email subject. If you can only provide a generic title for a noncommunication notification — like New Document — it can be better to let the system display your app name instead. Use title-style capitalization and no ending punctuation."

**Body**
- "Write succinct, easy-to-read notification content. Use complete sentences, sentence case, and proper punctuation, and don't truncate your message — the system does this automatically when necessary."
- Hidden-preview placeholder: "write body text that succinctly describes the notification content without revealing too many details, like "Friend request," "New comment," "Reminder," or "Shipment"… Use sentence-style capitalization for this text." (When previews are hidden the system shows "the default title Notification".)
- "Avoid including your app name or icon."
- "Avoid including sensitive, personal, or confidential information in a notification."
- "Avoid sending a notification that tells people to perform specific tasks within your app. … avoid telling people what to do because it's hard for people to remember such instructions after they dismiss the notification."
- "Use an alert — not a notification — to display an error message."

**Notification action buttons**
- "For each button, use a short, title-case term or phrase that clearly describes the result of the action. Don't include your app name or any extraneous information in the button label, keep the text brief to avoid truncation, and take localization into account as you write it."
- "Avoid providing an action that merely opens your app."
- "Prefer nondestructive actions. If you must provide a destructive action, make sure people have enough context to avoid unintended consequences."

---

## Privacy — purpose strings / permission wording

- "Write copy that clearly describes how your app uses the ability, data, or resource you're requesting. The standard alert displays your copy (called a purpose string or usage description string) after your app name and before the buttons people use to grant or deny their permission. Aim for a brief, complete sentence that's straightforward, specific, and easy to understand. Use sentence case, avoid passive voice, and include a period at the end."

Apple's table, verbatim (✓/✗ are checkmark/crossout images in the source):

| | Example purpose string | Notes |
| --- | --- | --- |
| ✓ | "The app records during the night to detect snoring sounds." | "An active sentence that clearly describes how and why the app collects the data." |
| ✗ | "Microphone access is needed for a better experience." | "A passive sentence that provides a vague, undefined justification." |
| ✗ | "Turn on microphone access." | "An imperative sentence that doesn't provide any justification." |

**Pre-alert screens**
- "Include only one button and make it clear that it opens the system alert. … Another type of manipulation is using a term like "Allow" to title the custom screen's button. If the custom button seems similar in meaning and visual weight to the allow button in the alert, people can be more likely to choose the alert's allow button without meaning to. Use a term like "Continue" or "Next" to title the single button in your custom screen or window, clarifying that its action is to open the system alert."
- "Don't include additional actions in your custom screen or window. For example, don't provide a way for people to leave the screen or window without viewing the system alert — like offering an option to close or cancel."
- "Never precede the system-provided alert with a custom screen or window that could confuse or mislead people." Prohibited designs named: "offering incentives, displaying a screen or window that looks like a request, displaying an image of the alert, and annotating the screen behind the alert".

**Timing** — "Request permission only when your app clearly needs access to the data or resource. … Avoid requesting permission at launch unless the data or resource is required for your app to function."

**Location button titles** — "Choose the system-provided title that works best with your feature, such as "Current Location" or "Share My Current Location."" Text "needs to fit without truncation at all accessibility text sizes and when translated into other languages."

---

## Managing accounts

- "Explain the benefits of creating an account and how to sign up. If your app or game requires an account, write a brief, friendly description of the reasons for the requirement and its benefits. Display this message in your sign-in view."
- "Always identify the authentication method you offer. For example, if you display a button for signing in to your app with Face ID, title it using a phrase like "Sign In with Face ID" instead of a generic phrase like "Sign In.""
- "Refer only to authentication methods that are available in the current context. For example, don't reference Face ID on a device that doesn't offer it."
- "Avoid using the term *passcode* to refer to account authentication. People create a passcode to unlock their device or authenticate for Apple services. If you use the term in your interface, people might think you're asking them to reuse their passcode in your app or game."
- Account deletion: "If legal requirements compel your app to maintain accounts or information … clearly describe the situation so people can understand the information or accounts you must maintain and the process you must follow."
- "Provide a clear way to initiate account deletion within your app or game. … Make the link easy to discover — for example, don't bury it in your Privacy Policy or Terms of Service pages."
- "Tell people when account deletion will complete, and notify them when it's finished."
- tvOS: "Never instruct people to sign out by adjusting privacy controls."

---

## In-app purchase / subscriptions

- "Use simple, succinct product names and descriptions. Titles that don't truncate or wrap and plain, direct language can help people find products quickly."
- "Display the total billing price for each in-app purchase you offer, regardless of type."
- "Provide clear, distinguishable subscription options. Use short, self-explanatory names that differentiate subscription options from one another, and specify the price and duration for each option. If you offer an introductory price, be sure to list the introductory price, the duration of the offer, and the standard price the customer pays after the offer ends."
- Sign-up screen must include: "The subscription name, duration, and the content or services provided during each subscription period"; "The billing amount, correctly localized…"; "A way for existing subscribers to sign in or restore purchases".
- "Clearly describe how a free trial works. It's particularly important to make sure people know that when the free trial is over, a payment will be automatically initiated for the next subscription period."
- Refunds: "Use a simple title for the refund action, like "Refund" or "Request a Refund". The system-provided refund flow makes it clear that people request a refund from Apple, so there's no need to reiterate this information."
- "Avoid characterizing or providing guidance on Apple's refund policies. For example, don't speculate about whether customers will receive the refund they request."
- Family Sharing: "including "Family" or "Shareable" in a subscription or item name"; in-app messaging "might welcome them with wording like "Your family subscription includes…"."
- Offer codes: "you could add a "Redeem Code" button to your paywall, onboarding screens, or your app's settings screen." "Clearly explain offer details… provide a straightforward and succinct description of your offer."
- Cancellation: "Always make it easy for customers to cancel an auto-renewable subscription. If the manage subscription action is deep within an app — or hard to recognize — subscribers can feel they're being discouraged or prevented from canceling."
- watchOS: "the button's title can update to reflect the chosen option."

---

## Settings

- macOS: "Include a settings item in the App menu. … If you provide document-level options, add this item to your app's File menu."
- macOS: "Update the window's title to reflect the currently visible pane. If your settings window doesn't have multiple panes, use the title *App Name* Settings."
- macOS: "use a noncustomizable toolbar that remains visible and always indicates the active toolbar button."
- "Minimize the number of settings you offer."
- "Respect people's systemwide settings and avoid including redundant versions of them in your custom settings area."
- "Avoid using settings to ask for setup information you can get in other ways."
- watchOS: consider "letting people use a More menu to reconfigure objects."

---

## Onboarding

- "Keep onboarding content focused on the experience you provide. People enter your onboarding flow to learn about your app or game; they don't need to learn how to use the system or the device."
- "Consider providing a collection of context-specific tips instead of a single onboarding flow. … When you have instructional content that refers to a specific area of the interface, display these instructions near that area."
- "If you need to present a prerequisite onboarding flow, design a brief, enjoyable experience that doesn't require people to memorize a lot of information."
- "If you let people skip the tutorial when they first launch your app or game, don't present it again on subsequent launches, but make sure it's easy for people to find if they want to view it later."
- "If you need to include a splash screen, design a beautiful graphic that communicates succinctly."
- "Avoid displaying licensing details within your onboarding flow."
- "If your app or game needs access to private data or resources before it can function, consider integrating the permission request into your onboarding flow… gives you the opportunity to show people why your app or game needs their permission and the benefits of granting it."

---

## Loading / progress messaging

- "Show something as soon as possible. If you make people wait for loading to complete before displaying anything, they can interpret the lack of content as a problem with your app or game. Instead, consider showing placeholder text, graphics, or animations as content loads, replacing these elements as content becomes available."
- "Clearly communicate that content is loading and how long it might take to complete. … you use a determinate progress indicator when you know how long loading will take, and you use an indeterminate progress indicator when you don't."
- "If loading takes an unavoidably long time, give people something interesting to view while they wait. For example, you might provide gameplay hints, display tips, or introduce people to new features."
- watchOS: "In situations where content needs a second or two to load, it's better to display a loading indicator than a blank screen."

---

## Feedback

- "Show people when a command can't be carried out and help them understand why. For example, if people request directions without specifying a destination, Maps tells them that it can't provide directions to and from the same location."
- "Warn people when they initiate a task that can cause data loss that's unexpected and irreversible. In contrast, don't warn people when data loss is the expected result of their action. For example, the Finder doesn't warn people every time they throw away a file because deleting the file is the expected result."
- "When it makes sense, confirm that a significant action or task has completed. … It's generally best to reserve this type of confirmation for activities that are sufficiently important — because people typically expect their action or task to succeed, they only need to know when it doesn't."
- "Use alerts to deliver critical — and ideally actionable — information. … Alerts can lose their impact if you use them too often or to deliver unimportant information."
- "Consider integrating status feedback into your interface." Example: "Mail in iOS and iPadOS describes the most recent update and displays the number of unread messages in the toolbar of the mailbox screen."
- "Make sure all feedback is accessible. … when you provide feedback using color, text, sound, and haptics, people can receive it whether they silence their device, look away from the screen, or use VoiceOver."
- watchOS: "reassure people that they'll receive a notification when the process completes."

---

## Undo and redo (wording)

- "If you provide undo and redo menu items, you can modify the menu item labels to identify the result. For example, a document-based app might use menu item labels like Undo Typing or Redo Bold."
- iOS/iPadOS shake alert: "Briefly and precisely describe the operation to be undone or redone. The undo and redo alert title automatically includes a prefix of "Undo " or "Redo " (including the trailing space). You need to provide an additional word or two that describes what's being undone or redone, to appear after this prefix. For example, you might create alert titles such as "Undo Name" or "Redo Address Change.""
- macOS: "Place undo and redo commands in the Edit menu and support the standard keyboard shortcuts."

---

## Searching

- "Clearly display the current scope of a search. Use a descriptive placeholder text, a scope bar, or a title to help reinforce what someone is currently searching. For example, in the Mail app there is always a clear reference to the mailbox someone is searching."
- "Provide suggestions to make searching easier. When you display a person's recent searches before they start typing or offer predictive search suggestions while they're typing, you can help people search faster and type less."
- "Aim to make your app's content searchable through a single location."
- "Take privacy into consideration before displaying search history. … If you do show search history, provide a way for people to clear it if they want."
- Spotlight: "Define metadata for custom file types you handle."

---

# Part 3: Cross-cutting summary

- **Title-style capitalization**: button titles, menu-item labels, menu-bar menu titles, notification titles, notification action button labels.
- **Sentence-style capitalization**: alert informative text; alert titles that are complete sentences; notification body text; hidden-preview placeholder text; permission purpose strings.
- **No ending punctuation**: button titles; notification titles; alert titles that are sentence fragments. **With ending punctuation**: alert informative text; purpose strings (period required); alert titles that are complete sentences.
- **Ellipsis (…)** appended when the action needs more input before it completes — menu items, macOS push buttons that open another window/view/app, and the standard `Settings…`, `Rename…`, `Move To…`, `Export As…`, `Page Setup…`, `Print…` items.
- **Verbs**: menu items and button labels prefer verbs/verb phrases naming the result ("View All," "Reply," "Ignore," "Add to Cart," "Erase," "Convert," "Clear," "Delete").
- **Avoid**: "Yes"/"No" in alerts; "OK" outside purely informational alerts; "Error"/"Error 329347 occurred" as titles; app name in window titles or notification text; the term "passcode" for account auth; "Allow" as a pre-permission screen button; text labels reading "Back" or "Close" on the standard toolbar buttons.
- **Always**: "Cancel" is the exact title for a button that cancels an alert's action; "Done" for a single-button default alert.
