DELETE FROM "public"."chart_ref" WHERE ("location" = 'cronjob-chart_1-2-0' AND "version" = '1.2.0');

UPDATE "public"."chart_ref" SET "is_default" = 't' WHERE "location" = 'reference-chart_3-12-0' AND "version" = '3.12.0';