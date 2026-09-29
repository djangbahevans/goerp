import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  continueMentionQuery,
  encodeMentions,
  mentionLabel,
  mentionName,
  mentionQueryStartedAt,
  renderCommentBody,
} from "./chatter-mentions.js";

const AMA = "0196f3a2-0000-7000-8000-000000000001";
const AMA_OWUSU = "0196f3a2-0000-7000-8000-000000000002";
const KOFI = "0196f3a2-0000-7000-8000-000000000003";

describe("mentionQueryStartedAt", () => {
  it("starts a query for an @ at the start of the text or after whitespace", () => {
    expect(mentionQueryStartedAt("@", 1)).toEqual({ start: 0, query: "" });
    expect(mentionQueryStartedAt("Hi @", 4)).toEqual({ start: 3, query: "" });
    expect(mentionQueryStartedAt("Hi\n@", 4)).toEqual({ start: 3, query: "" });
  });

  it("doesn't start one for an @ inside a word, such as an email address", () => {
    expect(mentionQueryStartedAt("ama@", 4)).toBeNull();
    expect(mentionQueryStartedAt("Hi @a", 5)).toBeNull();
  });
});

describe("continueMentionQuery", () => {
  it("is the text after the @ up to the caret, single spaces included", () => {
    expect(continueMentionQuery("Hi @Ama Ow", 3, 10)).toEqual({ start: 3, query: "Ama Ow" });
  });

  it("ends on a line break, a second space, a leading space, 50 characters, or a lost @", () => {
    expect(continueMentionQuery("@Ama\n", 0, 5)).toBeNull();
    expect(continueMentionQuery("@Ama  ", 0, 6)).toBeNull();
    expect(continueMentionQuery("@ ", 0, 2)).toBeNull();
    expect(continueMentionQuery(`@${"a".repeat(51)}`, 0, 52)).toBeNull();
    expect(continueMentionQuery(`@${"a".repeat(50)}`, 0, 51)?.query).toHaveLength(50);
    expect(continueMentionQuery("Ama", 0, 3)).toBeNull();
  });

  it("ends when the caret moves back over the @", () => {
    expect(continueMentionQuery("Hi @Ama", 3, 3)).toBeNull();
  });
});

describe("encodeMentions", () => {
  it("encodes every occurrence of a recorded name", () => {
    const recorded = new Map([["Kofi Boateng", KOFI]]);
    expect(encodeMentions("@Kofi Boateng, see @Kofi Boateng's note", recorded)).toBe(
      `<@${KOFI}>, see <@${KOFI}>'s note`,
    );
  });

  it("never reads @Amanda or @Ama Owusu as a mention of Ama", () => {
    const recorded = new Map([
      ["Ama", AMA],
      ["Ama Owusu", AMA_OWUSU],
    ]);
    expect(encodeMentions("@Ama Owusu and @Ama, not @Amanda", recorded)).toBe(
      `<@${AMA_OWUSU}> and <@${AMA}>, not @Amanda`,
    );
  });

  it("leaves an @ inside a word, or a recorded name the user has since edited, as plain text", () => {
    const recorded = new Map([["Ama", AMA]]);
    expect(encodeMentions("mail x@Ama or @Am", recorded)).toBe("mail x@Ama or @Am");
    expect(encodeMentions("@Ama2 and @Ama.", recorded)).toBe(`@Ama2 and <@${AMA}>.`);
  });
});

describe("mentionName", () => {
  it("is the candidate's name, or their email's local part when they have none", () => {
    expect(mentionName({ name: "Ama Owusu", email: "ama@acme.example" })).toBe("Ama Owusu");
    expect(mentionName({ name: null, email: "ama@acme.example" })).toBe("ama");
  });
});

describe("renderCommentBody", () => {
  const mentions = [
    { id: AMA, name: "Ama Owusu", email: "ama@acme.example" },
    { id: KOFI, name: null, email: "kofi@acme.example" },
    { id: AMA_OWUSU, name: null, email: null },
  ];

  it("renders each token as @name, with the email and Unknown user fallbacks", () => {
    const body = `<@${AMA}> ask <@${KOFI}>, <@${AMA_OWUSU}> and <@0196f3a2-0000-7000-8000-00000000000f>`;
    const { container } = render(<p>{renderCommentBody(body, mentions, undefined)}</p>);
    expect(container.textContent).toBe("@Ama Owusu ask @kofi, @Unknown user and @Unknown user");
  });

  it("highlights only the viewer's own mentions", () => {
    const { container } = render(<p>{renderCommentBody(`<@${AMA}> and <@${KOFI}>`, mentions, KOFI)}</p>);
    const spans = container.querySelectorAll("span");
    expect(spans[0]?.className).not.toContain("bg-primary-subtle");
    expect(spans[1]?.className).toContain("bg-primary-subtle");
  });

  it("leaves text with no tokens as it is", () => {
    expect(renderCommentBody("Plain <@not-an-id> text", mentions, undefined)).toEqual(["Plain <@not-an-id> text"]);
  });
});

describe("mentionLabel", () => {
  it("falls back to Unknown user for an id missing from the list", () => {
    expect(mentionLabel(AMA, [])).toBe("Unknown user");
  });
});
